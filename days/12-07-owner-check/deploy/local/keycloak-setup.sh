#!/bin/sh
# Keycloak に、テナント A とテナント B に見立てた realm を2つ作り、claude.ai が DCR で自分を登録できるようにする。jq を使う。
#   tenant-a：alice と bob
#   tenant-b：carol
# 開発モードの Keycloak はコンテナを作り直すと中身が消えるので、立ち上げるたびに流す。
set -eu

KC=${KC:-http://127.0.0.1:8180}
# MCP サーバーの URL。アクセストークンの aud に入れる。トンネルを立てるまでわからないので、MCP_URL で受け取る。
MCP_URL=${MCP_URL:-http://127.0.0.1:8080/mcp}
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

# user REALM NAME：ユーザーを作り（なければ）、パスワードをランダムに作り直す。
user() {
	realm=$1 name=$2
	if [ -z "$(api "$KC/admin/realms/$realm/users?username=$name&exact=true" | jq -r '.[0].id // empty')" ]; then
		api -o /dev/null -d "{\"username\":\"$name\",\"enabled\":true,\"email\":\"$name@example.com\",\"emailVerified\":true,\"firstName\":\"$name\",\"lastName\":\"Example\"}" \
			"$KC/admin/realms/$realm/users"
	fi
	id=$(api "$KC/admin/realms/$realm/users?username=$name&exact=true" | jq -r '.[0].id')
	pw=$(LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 16)
	api -o /dev/null -X PUT -d "{\"type\":\"password\",\"value\":\"$pw\",\"temporary\":false}" \
		"$KC/admin/realms/$realm/users/$id/reset-password"
	echo "$realm $name $pw" >> "$PASSWORDS"
}

# realm REALM：realm を作り、DCR と aud の設定をする。
realm() {
	REALM=$1
	if ! api -o /dev/null "$KC/admin/realms/$REALM"; then
		# 起動した直後は、realm の作成が失敗することがあるので、できるまで繰り返す。
		until api -o /dev/null -d "{\"realm\":\"$REALM\",\"enabled\":true}" "$KC/admin/realms" || api -o /dev/null "$KC/admin/realms/$REALM"; do sleep 2; done
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

realm tenant-a
user tenant-a alice
user tenant-a bob
realm tenant-b
user tenant-b carol

echo "realm「tenant-a」「tenant-b」の準備ができました（aud: ${MCP_URL}）"
echo "ログインに使うユーザーとパスワード（${PASSWORDS} にもあります）："
sed 's/^/  /' "$PASSWORDS"
