#!/bin/sh
# Keycloak に realm を3つ作り、claude.ai が DCR で自分を登録できるようにする。jq を使う。
#   やり方1（テナントごとに realm）
#     tenant-a：alice、bob、dave
#     tenant-b：carol、dave        ← dave は realm ごとに別のアカウント
#   やり方2（realm を共有して、中に組織を作る）
#     shared  ：alice（組織 tenant-a）、dave（組織 tenant-a と tenant-b）
# 開発モードの Keycloak はコンテナを作り直すと中身が消えるので、立ち上げるたびに流す。
set -eu

KC=${KC:-http://127.0.0.1:8180}
# MCP サーバーの URL。アクセストークンの aud に入れる。やり方2 用と、やり方1 のテナントごと（ホストが違う）。
# トンネルを立てるまでわからないので、MCP_URL_SHARED、MCP_URL_A、MCP_URL_B で受け取る。
MCP_URL_SHARED=${MCP_URL_SHARED:-http://127.0.0.1:8080/mcp}
MCP_URL_A=${MCP_URL_A:-http://tenant-a.localhost:8080/mcp}
MCP_URL_B=${MCP_URL_B:-http://tenant-b.localhost:8080/mcp}
# 12/7 から、メモの中身を伏せないので、パスワードは流すたびにランダムに作る。ここに書き出す（.gitignore 済み）。
PASSWORDS=${PASSWORDS:-$(dirname "$0")/../../.passwords}

echo "Keycloak の起動を待っています…"
until curl -sf -o /dev/null "$KC/realms/master"; do sleep 2; done

# 管理者のトークンは1分で切れるので、管理 API を呼ぶたびに取り直す。
admin_token() {
	curl -sf -d client_id=admin-cli -d username=admin -d password=admin -d grant_type=password \
		"$KC/realms/master/protocol/openid-connect/token" | jq -re .access_token
}
api() { curl -sf -H "Authorization: Bearer $(admin_token)" -H 'Content-Type: application/json' "$@"; }
# 起動した直後は、管理 API がまだ応えないことがあるので、応えるまで待つ。
until api -o /dev/null "$KC/admin/realms/master"; do sleep 2; done

# 管理画面は master realm でログインするが、master realm は外に出していない。
# master realm だけ、外から見える URL を手元の URL にして、管理画面を手元から使えるようにする。
api -o /dev/null -X PUT -d "{\"attributes\":{\"frontendUrl\":\"$KC\"}}" "$KC/admin/realms/master"

: > "$PASSWORDS"

# user_id REALM NAME：ユーザーの ID を出す（いなければ空）。
user_id() {
	api "$KC/admin/realms/$1/users?username=$2&exact=true" | jq -r '.[0].id // empty'
}

# user REALM NAME：ユーザーを作り（なければ）、パスワードをランダムに作り直す。
user() {
	realm=$1 name=$2
	if [ -z "$(user_id "$realm" "$name")" ]; then
		api -o /dev/null -d "{\"username\":\"$name\",\"enabled\":true,\"email\":\"$name@example.com\",\"emailVerified\":true,\"firstName\":\"$name\",\"lastName\":\"Example\"}" \
			"$KC/admin/realms/$realm/users"
	fi
	id=$(user_id "$realm" "$name")
	pw=$(LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 16)
	api -o /dev/null -X PUT -d "{\"type\":\"password\",\"value\":\"$pw\",\"temporary\":false}" \
		"$KC/admin/realms/$realm/users/$id/reset-password"
	echo "$realm $name $pw" >> "$PASSWORDS"
}

# realm REALM MCP_URL [organizations]：realm を作り、DCR と aud の設定をする。
# MCP_URL は、この realm のトークンを受け付ける MCP サーバーの URL。
# 3つ目に organizations を渡すと、Organizations をオンにする。
realm() {
	REALM=$1 MCP_URL=$2
	if ! api -o /dev/null "$KC/admin/realms/$REALM"; then
		# 起動した直後は、realm の作成が失敗することがあるので、できるまで繰り返す。
		until api -o /dev/null -d "{\"realm\":\"$REALM\",\"enabled\":true}" "$KC/admin/realms" || api -o /dev/null "$KC/admin/realms/$REALM"; do sleep 2; done
	fi

	# Organizations をオンにすると、organization スコープが realm にできる。
	# あとで scopes_supported を読むので、先にオンにしておく。
	if [ "${3:-}" = organizations ]; then
		api -o /dev/null -X PUT -d '{"organizationsEnabled":true}' "$KC/admin/realms/$REALM"
	fi

	# 認証なしの DCR は「Trusted Hosts」のポリシーが止めている。
	# 送り元のホストは見ない（nginx で Anthropic の IP に絞っている）。そのかわり、redirect_uris は claude.ai だけにする。
	POLICY=$(api "$KC/admin/realms/$REALM/components?type=org.keycloak.services.clientregistration.policy.ClientRegistrationPolicy&name=Trusted%20Hosts")
	ANON=$(echo "$POLICY" | jq -c '.[] | select(.subType=="anonymous")')
	ID=$(echo "$ANON" | jq -r .id)
	api -o /dev/null -X PUT -d "$(echo "$ANON" | jq -c '
		.config["host-sending-registration-request-must-match"] = ["false"]
		| .config["trusted-hosts"] = ["claude.ai"]
		| .config["client-uris-must-match"] = ["true"]')" \
		"$KC/admin/realms/$REALM/components/$ID"

	# アクセストークンの aud に、MCP サーバーの URL を入れる（12/6）。
	# 12/8 から、realm ごとに URL（ホスト）が違う。テナント A のトークンは、テナント A のホスト宛てになる。
	# claude.ai は DCR で毎回新しいクライアントとして登録されるので、マッパーは realm の既定のクライアントスコープに置く。
	SCOPE=mcp-audience
	SCOPE_ID=$(api "$KC/admin/realms/$REALM/client-scopes" | jq -r --arg n "$SCOPE" '.[] | select(.name==$n) | .id')
	if [ -z "$SCOPE_ID" ]; then
		api -o /dev/null -d "{\"name\":\"$SCOPE\",\"protocol\":\"openid-connect\",\"attributes\":{\"include.in.token.scope\":\"false\",\"display.on.consent.screen\":\"false\"}}" \
			"$KC/admin/realms/$REALM/client-scopes"
		SCOPE_ID=$(api "$KC/admin/realms/$REALM/client-scopes" | jq -r --arg n "$SCOPE" '.[] | select(.name==$n) | .id')
		api -o /dev/null -X PUT "$KC/admin/realms/$REALM/default-default-client-scopes/$SCOPE_ID"
	fi
	for M in $(api "$KC/admin/realms/$REALM/client-scopes/$SCOPE_ID/protocol-mappers/models" | jq -r '.[].id'); do
		api -o /dev/null -X DELETE "$KC/admin/realms/$REALM/client-scopes/$SCOPE_ID/protocol-mappers/models/$M"
	done
	api -o /dev/null -d "{\"name\":\"audience\",\"protocol\":\"openid-connect\",\"protocolMapper\":\"oidc-audience-mapper\",\"config\":{\"included.custom.audience\":\"$MCP_URL\",\"access.token.claim\":\"true\",\"id.token.claim\":\"false\"}}" \
		"$KC/admin/realms/$REALM/client-scopes/$SCOPE_ID/protocol-mappers/models"

	# 「Allowed Client Scopes」のポリシーは、登録で頼めるスコープを realm の既定のクライアントスコープに限っている。
	# claude.ai は、メタデータの scopes_supported に並んだスコープをまとめて頼んでくるので、それを全部許す。
	SUPPORTED=$(curl -sf "$KC/realms/$REALM/.well-known/openid-configuration" | jq -c .scopes_supported)
	SCOPES=$(api "$KC/admin/realms/$REALM/components?type=org.keycloak.services.clientregistration.policy.ClientRegistrationPolicy&name=Allowed%20Client%20Scopes")
	ANON=$(echo "$SCOPES" | jq -c '.[] | select(.subType=="anonymous")')
	ID=$(echo "$ANON" | jq -r .id)
	api -o /dev/null -X PUT -d "$(echo "$ANON" | jq -c --argjson s "$SUPPORTED" '.config["allowed-client-scopes"] = $s')" \
		"$KC/admin/realms/$REALM/components/$ID"
}

# org_id REALM NAME：組織の ID を出す（なければ空）。
org_id() {
	api "$KC/admin/realms/$1/organizations?search=$2&exact=true" | jq -r '.[0].id // empty'
}

# organization REALM NAME：組織（テナント）を作る（なければ）。
organization() {
	if [ -z "$(org_id "$1" "$2")" ]; then
		api -o /dev/null -d "{\"name\":\"$2\",\"alias\":\"$2\",\"enabled\":true}" \
			"$KC/admin/realms/$1/organizations"
	fi
}

# member REALM ORG USER：ユーザーを組織に所属させる。
# 所属は、管理者がここで決める。ユーザーが自分で書き換えられる値（メールアドレスなど）からは決めない。
member() {
	oid=$(org_id "$1" "$2")
	uid=$(user_id "$1" "$3")
	if [ -z "$(api "$KC/admin/realms/$1/organizations/$oid/members" | jq -r --arg u "$uid" '.[] | select(.id==$u) | .id')" ]; then
		api -o /dev/null -d "\"$uid\"" "$KC/admin/realms/$1/organizations/$oid/members"
	fi
}

# やり方1：テナントごとに realm を分ける。
realm tenant-a "$MCP_URL_A"
user tenant-a alice
user tenant-a bob
user tenant-a dave
realm tenant-b "$MCP_URL_B"
user tenant-b carol
user tenant-b dave

# やり方2：realm は1つ。テナントは、realm の中の組織。
realm shared "$MCP_URL_SHARED" organizations
user shared alice
user shared dave
organization shared tenant-a
organization shared tenant-b
member shared tenant-a alice
member shared tenant-a dave
member shared tenant-b dave

echo "realm「tenant-a」「tenant-b」「shared」の準備ができました（aud: ${MCP_URL_A}、${MCP_URL_B}、${MCP_URL_SHARED}）"
echo "ログインに使うユーザーとパスワード（${PASSWORDS} にもあります）："
sed 's/^/  /' "$PASSWORDS"
