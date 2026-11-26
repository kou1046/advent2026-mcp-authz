#!/bin/sh
# 立ち上げた MCP サーバーと Keycloak を curl で叩く。
# MCP サーバーのメタデータが Keycloak を指し、Keycloak がメタデータと DCR に応えることを確かめる。
set -eu

BASE=${BASE:-http://127.0.0.1:8080}
KC=${KC:-http://127.0.0.1:8180}
GATE_MCP=${GATE_MCP:-http://127.0.0.1:8081}
GATE_AS=${GATE_AS:-http://127.0.0.1:8082}

echo "--- MCP の URL（トークンなし → 401 と WWW-Authenticate）"
curl -s -i "$BASE/mcp" -H 'Content-Type: application/json' \
	-d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"curl","version":"0"}}}' | grep -iE '^HTTP|^www-authenticate'

INIT='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"curl","version":"0"}}}'

echo "--- MCP の URL（でたらめな Bearer → 401 と error=\"invalid_token\"）"
curl -s -i "$BASE/mcp" -H 'Content-Type: application/json' -H 'Authorization: Bearer not-a-real-token' -d "$INIT" | grep -iE '^HTTP|^www-authenticate'

# 本物のトークンを、手元用のクライアント（パスワードで直接もらえる）で出す。
ADMIN=$(curl -sf -d client_id=admin-cli -d username=admin -d password=admin -d grant_type=password \
	"$KC/realms/master/protocol/openid-connect/token" | jq -r .access_token)
curl -s -o /dev/null -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
	-d '{"clientId":"smoke","publicClient":true,"directAccessGrantsEnabled":true,"standardFlowEnabled":false}' "$KC/admin/realms/mcp/clients"
TOKEN=$(curl -sf -d client_id=smoke -d username=alice -d password=alice -d grant_type=password \
	"$KC/realms/mcp/protocol/openid-connect/token" | jq -r .access_token)

echo "--- 本物のトークンの中身（iss と aud）"
echo "$TOKEN" | jq -R -c 'split(".")[1] | gsub("-";"+") | gsub("_";"/") | @base64d | fromjson | {iss, aud, exp}'

echo "--- MCP の URL（本物のトークン → 200）"
curl -s -o /dev/null -w '[%{http_code}]\n' "$BASE/mcp" -H 'Content-Type: application/json' -H "Authorization: Bearer $TOKEN" -d "$INIT"

echo "--- MCP の URL（本物のトークンの中身を書き換える → 401、bad signature）"
FORGED=$(echo "$TOKEN" | awk -F. '{print $1"."$2"x."$3}')
curl -s -i "$BASE/mcp" -H 'Content-Type: application/json' -H "Authorization: Bearer $FORGED" -d "$INIT" | grep -iE '^www-authenticate'

echo "--- MCP サーバーのメタデータ（authorization_servers が Keycloak の realm。RESOURCE を渡していれば resource はその値）"
curl -s "$BASE/.well-known/oauth-protected-resource/mcp"; echo

echo "--- Keycloak のメタデータの置き場所（1：RFC 8414 差し込み、2：OIDC 差し込み、3：OIDC 後ろに足す）"
for p in /.well-known/oauth-authorization-server/realms/mcp /.well-known/openid-configuration/realms/mcp /realms/mcp/.well-known/openid-configuration; do
	curl -s -o /dev/null -w "%{http_code} $p\n" "$KC$p"
done

echo "--- DCR（claude.ai と同じ形の登録 → 201）"
REG='{"redirect_uris":["https://claude.ai/api/mcp/auth_callback"],"token_endpoint_auth_method":"none","grant_types":["authorization_code","refresh_token"],"response_types":["code"],"client_name":"smoke","application_type":"web"}'
curl -s -o /dev/null -w '[%{http_code}]\n' -H 'Content-Type: application/json' -d "$REG" "$KC/realms/mcp/clients-registrations/openid-connect"

echo "--- DCR（よその redirect_uri → 403）"
curl -s -o /dev/null -w '[%{http_code}]\n' -H 'Content-Type: application/json' -d "$(echo "$REG" | sed 's#claude.ai/api#evil.example.com/api#')" "$KC/realms/mcp/clients-registrations/openid-connect"

echo "--- nginx 越し（claude.ai の範囲外：メタデータは 403、管理画面は 404、ログイン画面の入口は誰でも通す）"
curl -s -o /dev/null -w 'MCP のホスト /mcp: [%{http_code}]\n' "$GATE_MCP/mcp" -d '{}'
curl -s -o /dev/null -w '認可サーバーのホスト メタデータ: [%{http_code}]\n' "$GATE_AS/realms/mcp/.well-known/openid-configuration"
curl -s -o /dev/null -w '認可サーバーのホスト /admin: [%{http_code}]\n' "$GATE_AS/admin/"
curl -s -o /dev/null -w '認可サーバーのホスト ログイン画面の入口: [%{http_code}]\n' "$GATE_AS/realms/mcp/protocol/openid-connect/auth?client_id=account-console&response_type=code&redirect_uri=$GATE_AS/realms/mcp/account/&code_challenge=E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM&code_challenge_method=S256"
