#!/bin/sh
# 立ち上げた MCP サーバーと Keycloak を curl で叩く。
# 本物のトークンで、自分のメモだけ読み書きできること（他人のメモは「見つからない」）を確かめる。
set -eu

BASE=${BASE:-http://127.0.0.1:8080}
KC=${KC:-http://127.0.0.1:8180}
PASSWORDS=${PASSWORDS:-$(dirname "$0")/../../.passwords}

ADMIN=$(curl -sf -d client_id=admin-cli -d username=admin -d password=admin -d grant_type=password \
	"$KC/realms/master/protocol/openid-connect/token" | jq -r .access_token)

# token REALM USER：手元用のクライアント（パスワードで直接もらえる）で、本物のトークンを出す。
token() {
	curl -s -o /dev/null -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
		-d '{"clientId":"smoke","publicClient":true,"directAccessGrantsEnabled":true,"standardFlowEnabled":false}' "$KC/admin/realms/$1/clients"
	pw=$(awk -v r="$1" -v u="$2" '$1==r && $2==u {print $3}' "$PASSWORDS")
	curl -sf -d client_id=smoke -d username="$2" -d password="$pw" -d grant_type=password \
		"$KC/realms/$1/protocol/openid-connect/token" | jq -r .access_token
}

# call TOKEN TOOL ARGS：ツールを呼んで、結果の中身（structuredContent か、エラーの文）を出す。
call() {
	curl -s "$BASE/mcp" -H 'Content-Type: application/json' -H "Authorization: Bearer $1" \
		-d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"$2\",\"arguments\":$3}}" |
		jq -c '.result | if .isError then {error: .content[0].text} else .structuredContent end'
}

echo "--- MCP の URL（トークンなし → 401 と WWW-Authenticate）"
curl -s -i "$BASE/mcp" -H 'Content-Type: application/json' -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | grep -iE '^HTTP|^www-authenticate'

echo "--- MCP サーバーのメタデータ（authorization_servers に、REALMS の順で並ぶ）"
curl -s "$BASE/.well-known/oauth-protected-resource/mcp" | jq -c .authorization_servers

ALICE=$(token tenant-a alice)
BOB=$(token tenant-a bob)
CAROL=$(token tenant-b carol)

echo "--- alice がメモを書く（持ち主は alice の iss と sub）"
ID=$(call "$ALICE" write_note '{"title":"買い物メモ","body":"牛乳"}' | tee /dev/stderr | jq -r .id)

echo "--- alice の一覧（自分のメモが見える）"
call "$ALICE" read_note '{}'

echo "--- bob の一覧（同じテナントでも、alice のメモは見えない）"
call "$BOB" read_note '{}'

echo "--- bob が alice のメモの ID を指定する（ないメモと同じく、見つからない）"
call "$BOB" read_note "{\"id\":\"$ID\"}"

echo "--- carol（別のテナント）の一覧"
call "$CAROL" read_note '{}'

echo "--- DB の中身（持ち主の列）"
docker compose -f "$(dirname "$0")/compose.yaml" exec -T db psql -U mcp -c 'SELECT id, owner_iss, owner_sub, title FROM notes'
