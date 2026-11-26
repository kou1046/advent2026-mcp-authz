#!/bin/sh
# Keycloak に realm「mcp」を作り、claude.ai が DCR で自分を登録できるようにする。jq を使う。
# 開発モードの Keycloak はコンテナを作り直すと中身が消えるので、立ち上げるたびに流す。
set -eu

KC=${KC:-http://127.0.0.1:8180}
REALM=mcp

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

# realm とダミーユーザー（ログイン画面で使う）
if ! api -o /dev/null "$KC/admin/realms/$REALM"; then
	# 起動した直後は、realm の作成が失敗することがあるので、できるまで繰り返す。
	until api -o /dev/null -d "{\"realm\":\"$REALM\",\"enabled\":true}" "$KC/admin/realms" || api -o /dev/null "$KC/admin/realms/$REALM"; do sleep 2; done
	api -o /dev/null -d '{"username":"alice","enabled":true,"email":"alice@example.com","emailVerified":true,"firstName":"Alice","lastName":"Example","credentials":[{"type":"password","value":"alice","temporary":false}]}' \
		"$KC/admin/realms/$REALM/users"
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
# Keycloak は default だと resource を聞き流すので、Audience マッパーで固定値を入れる。
# claude.ai は DCR で毎回新しいクライアントとして登録されるので、マッパーは realm の既定のクライアントスコープに置き、
# 登録されたクライアントに自動で付くようにする。
# MCP サーバーの URL はトンネルを立てるまでわからないので、MCP_URL で受け取る。
MCP_URL=${MCP_URL:-http://127.0.0.1:8080/mcp}
SCOPE=mcp-audience
SCOPE_ID=$(api "$KC/admin/realms/$REALM/client-scopes" | jq -r --arg n "$SCOPE" '.[] | select(.name==$n) | .id')
if [ -z "$SCOPE_ID" ]; then
	# include.in.token.scope を false にして、scope の値やメタデータの scopes_supported には出さない（aud を足すだけ）。
	api -o /dev/null -d "{\"name\":\"$SCOPE\",\"protocol\":\"openid-connect\",\"attributes\":{\"include.in.token.scope\":\"false\",\"display.on.consent.screen\":\"false\"}}" \
		"$KC/admin/realms/$REALM/client-scopes"
	SCOPE_ID=$(api "$KC/admin/realms/$REALM/client-scopes" | jq -r --arg n "$SCOPE" '.[] | select(.name==$n) | .id')
	api -o /dev/null -X PUT "$KC/admin/realms/$REALM/default-default-client-scopes/$SCOPE_ID"
fi
# マッパーは、URL が変わるたびに作り直す。
for M in $(api "$KC/admin/realms/$REALM/client-scopes/$SCOPE_ID/protocol-mappers/models" | jq -r '.[].id'); do
	api -o /dev/null -X DELETE "$KC/admin/realms/$REALM/client-scopes/$SCOPE_ID/protocol-mappers/models/$M"
done
api -o /dev/null -d "{\"name\":\"audience\",\"protocol\":\"openid-connect\",\"protocolMapper\":\"oidc-audience-mapper\",\"config\":{\"included.custom.audience\":\"$MCP_URL\",\"access.token.claim\":\"true\",\"id.token.claim\":\"false\"}}" \
	"$KC/admin/realms/$REALM/client-scopes/$SCOPE_ID/protocol-mappers/models"

# 「Allowed Client Scopes」のポリシーは、登録で頼めるスコープを realm の既定のクライアントスコープに限っている。
# claude.ai は、メタデータの scopes_supported に並んだスコープをまとめて頼んでくる。
# そのうち openid と service_account は既定に入っていないので、そのままだと弾かれる。
# メタデータで「対応している」と言ったスコープは、登録でも頼めるようにする（上で足したスコープを含めるため、最後に流す）。
SUPPORTED=$(curl -sf "$KC/realms/$REALM/.well-known/openid-configuration" | jq -c .scopes_supported)
SCOPES=$(api "$KC/admin/realms/$REALM/components?type=org.keycloak.services.clientregistration.policy.ClientRegistrationPolicy&name=Allowed%20Client%20Scopes")
ANON=$(echo "$SCOPES" | jq -c '.[] | select(.subType=="anonymous")')
ID=$(echo "$ANON" | jq -r .id)
api -o /dev/null -X PUT -d "$(echo "$ANON" | jq -c --argjson s "$SUPPORTED" '.config["allowed-client-scopes"] = $s')" \
	"$KC/admin/realms/$REALM/components/$ID"

echo "realm「${REALM}」の準備ができました（ユーザー alice / パスワード alice、aud: ${MCP_URL}）"
