#!/bin/sh
# Keycloak に realm「mcp」を作り、claude.ai が DCR で自分を登録できるようにする。jq を使う。
# 開発モードの Keycloak はコンテナを作り直すと中身が消えるので、立ち上げるたびに流す。
set -eu

KC=${KC:-http://127.0.0.1:8180}
REALM=mcp

echo "Keycloak の起動を待っています…"
until curl -sf -o /dev/null "$KC/realms/master"; do sleep 2; done

TOKEN=$(curl -sf -d client_id=admin-cli -d username=admin -d password=admin -d grant_type=password \
	"$KC/realms/master/protocol/openid-connect/token" | jq -r .access_token)
api() { curl -sf -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' "$@"; }

# realm とダミーユーザー（ログイン画面で使う）
if ! api -o /dev/null "$KC/admin/realms/$REALM"; then
	api -o /dev/null -d "{\"realm\":\"$REALM\",\"enabled\":true}" "$KC/admin/realms"
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

echo "realm「${REALM}」の準備ができました（ユーザー alice / パスワード alice）"
