#!/bin/sh
# 立ち上げた MCP サーバーと Keycloak を curl で叩く。
# 本物のトークンで、テナントと持ち主の両方で絞られていること（別のテナントのメモは「見つからない」）を確かめる。
set -eu

BASE=${BASE:-http://127.0.0.1:8080}
# やり方1 のテナントごとのホスト（compose.yaml の既定と同じ）。127.0.0.1 に繋いで、Host ヘッダーで名乗る。
HOST_A=tenant-a.localhost:8080
HOST_B=tenant-b.localhost:8080
HOST_S=127.0.0.1:8080
KC=${KC:-http://127.0.0.1:8180}
PASSWORDS=${PASSWORDS:-$(dirname "$0")/../../.passwords}

ADMIN=$(curl -sf -d client_id=admin-cli -d username=admin -d password=admin -d grant_type=password \
	"$KC/realms/master/protocol/openid-connect/token" | jq -r .access_token)

# token REALM USER [SCOPE]：手元用のクライアント（パスワードで直接もらえる）で、本物のトークンを出す。
token() {
	curl -s -o /dev/null -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
		-d '{"clientId":"smoke","publicClient":true,"directAccessGrantsEnabled":true,"standardFlowEnabled":false}' "$KC/admin/realms/$1/clients"
	pw=$(awk -v r="$1" -v u="$2" '$1==r && $2==u {print $3}' "$PASSWORDS")
	curl -sf -d client_id=smoke -d username="$2" -d password="$pw" -d grant_type=password ${3:+-d "scope=$3"} \
		"$KC/realms/$1/protocol/openid-connect/token" | jq -r .access_token
}

# claims TOKEN：トークンの中身から、テナントに関わるところだけ出す。
claims() {
	p=$(echo "$1" | cut -d. -f2 | tr '_-' '/+')
	while [ $((${#p} % 4)) -ne 0 ]; do p="$p="; done
	echo "$p" | base64 -d | jq -c '{iss: (.iss | sub(".*/realms/"; "…/realms/")), aud, preferred_username, scope, organization}'
}

# call HOST TOKEN TOOL ARGS：ツールを呼んで、結果の中身（structuredContent か、エラーの文）を出す。
call() {
	curl -s "$BASE/mcp" -H "Host: $1" -H 'Content-Type: application/json' -H "Authorization: Bearer $2" \
		-d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"$3\",\"arguments\":$4}}" |
		jq -c '.result | if .isError then {error: .content[0].text} else .structuredContent end'
}

echo "=== やり方1：URL で選ばせる（テナントごとのホスト）"

echo "--- トークンなし → 401。WWW-Authenticate は、そのホストのメタデータを指す"
curl -s -i "$BASE/mcp" -H "Host: $HOST_A" -H 'Content-Type: application/json' -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | grep -iE '^HTTP|^www-authenticate'

echo "--- メタデータ（ホストごと。authorization_servers は、そのテナントの realm だけ）"
for t in tenant-a tenant-b; do
	curl -s "$BASE/.well-known/oauth-protected-resource/mcp" -H "Host: $t.localhost:8080" | jq -c '{resource, authorization_servers}'
done

DAVE_A=$(token tenant-a dave)
DAVE_B=$(token tenant-b dave)
BOB_A=$(token tenant-a bob)

echo "--- dave のトークン（realm ごとに別のアカウント。iss も sub も違う）"
claims "$DAVE_A"
claims "$DAVE_B"

echo "--- dave が、テナント A とテナント B に、それぞれメモを書く"
ID_A=$(call "$HOST_A" "$DAVE_A" write_note '{"title":"A の議事録","body":"テナント A の話"}' | tee /dev/stderr | jq -r .id)
call "$HOST_B" "$DAVE_B" write_note '{"title":"B の議事録","body":"テナント B の話"}'

echo "--- dave の一覧（テナント A の URL → A のメモだけ）"
call "$HOST_A" "$DAVE_A" read_note '{}'
echo "--- dave の一覧（テナント B の URL → B のメモだけ）"
call "$HOST_B" "$DAVE_B" read_note '{}'

echo "--- テナント B の dave が、テナント A のメモの ID を指定する（見つからない）"
call "$HOST_B" "$DAVE_B" read_note "{\"id\":\"$ID_A\"}"

echo "--- bob（同じテナント A）の一覧（同じテナントでも、dave のメモは見えない）"
call "$HOST_A" "$BOB_A" read_note '{}'

echo "--- テナント A のトークンを、テナント B の URL に持っていく → 401"
curl -s -i "$BASE/mcp" -H "Host: $HOST_B" -H 'Content-Type: application/json' -H "Authorization: Bearer $DAVE_A" \
	-d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | grep -iE '^HTTP|^www-authenticate'

echo
echo "=== やり方2：ログイン画面で選ばせる（/mcp）"

echo "--- メタデータ（authorization_servers は realm shared だけ）"
curl -s "$BASE/.well-known/oauth-protected-resource/mcp" | jq -c '{resource, authorization_servers}'

echo "--- ツールの一覧（引数に tenant が増えている）"
ALICE=$(token shared alice organization)
curl -s "$BASE/mcp" -H 'Content-Type: application/json' -H "Authorization: Bearer $ALICE" \
	-d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | jq -c '.result.tools[] | {name, arguments: (.inputSchema.properties | keys)}'

echo "--- alice のトークン（組織は tenant-a だけ）"
claims "$ALICE"
echo "--- alice がメモを書く（組織が1つなので、それがテナントになる）"
call "$HOST_S" "$ALICE" write_note '{"title":"alice のメモ","body":"テナント A"}'
echo "--- alice が、属していない tenant-b を指定する（断る）"
call "$HOST_S" "$ALICE" read_note '{"tenant":"tenant-b"}'

# 2つの組織に属する dave のトークンに何が入るかは、スコープの頼み方で変わる。
for scope in organization 'organization:*' organization:tenant-b; do
	echo "--- dave のトークン（scope=${scope}）"
	DAVE=$(token shared dave "$scope") || DAVE=
	if [ -z "$DAVE" ]; then
		echo "（トークンが出なかった）"
		continue
	fi
	claims "$DAVE"
	echo "    tenant を指定せずに一覧"
	call "$HOST_S" "$DAVE" read_note '{}'
done

DAVE=$(token shared dave 'organization:*')
echo "--- dave（両方の組織が入ったトークン）が、tenant を指定して、それぞれにメモを書く"
call "$HOST_S" "$DAVE" write_note '{"tenant":"tenant-a","title":"A の議事録","body":"テナント A の話"}'
call "$HOST_S" "$DAVE" write_note '{"tenant":"tenant-b","title":"B の議事録","body":"テナント B の話"}'
echo "--- dave の一覧（tenant-a）"
call "$HOST_S" "$DAVE" read_note '{"tenant":"tenant-a"}'
echo "--- dave の一覧（tenant-b）"
call "$HOST_S" "$DAVE" read_note '{"tenant":"tenant-b"}'

echo "--- organization スコープを頼まなかったトークン（テナントがわからない）"
NONE=$(token shared dave)
claims "$NONE"
call "$HOST_S" "$NONE" read_note '{}'

echo
echo "--- DB の中身（テナントと持ち主の列）"
docker compose -f "$(dirname "$0")/compose.yaml" exec -T db psql -U mcp -c \
	"SELECT id, tenant, regexp_replace(owner_iss, '.*/realms/', '…/realms/') AS owner_iss, left(owner_sub, 8) AS owner_sub, title FROM notes ORDER BY created_at"
