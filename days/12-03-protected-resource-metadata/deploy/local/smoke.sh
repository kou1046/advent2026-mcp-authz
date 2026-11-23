#!/bin/sh
# 立ち上げた MCP サーバーと仮の認可サーバーを curl で叩く。
# トークンなしでは 401 になり、メタデータで別のホストの認可サーバーを教えることを確かめる。
set -eu

BASE=${BASE:-http://127.0.0.1:8080}
GATE_MCP=${GATE_MCP:-http://127.0.0.1:8081}
GATE_AS=${GATE_AS:-http://127.0.0.1:8082}
META='"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}'

echo "--- server/discover（トークンなし → 401 と WWW-Authenticate）"
curl -s -i "$BASE/mcp" \
	-H 'Content-Type: application/json' \
	-H 'Accept: application/json, text/event-stream' \
	-H 'MCP-Protocol-Version: 2026-07-28' \
	-H 'Mcp-Method: server/discover' \
	-d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"server/discover\",\"params\":{$META}}" | grep -iE '^HTTP|^www-authenticate'

echo "--- 古い版の initialize（Bearer 付き → 401 と invalid_token）"
curl -s -i "$BASE/mcp" -H 'Content-Type: application/json' -H 'Authorization: Bearer dummy' \
	-d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"curl","version":"0"}}}' | grep -iE '^HTTP|^www-authenticate'

echo "--- MCP サーバーのメタデータ（RFC 9728。authorization_servers は別のホスト）"
curl -s -w ' [%{http_code}]\n' "$BASE/.well-known/oauth-protected-resource/mcp"

echo "--- nginx 越し（claude.ai の範囲外なので、どちらのホストも 403 のはず）"
curl -s -o /dev/null -w 'MCP のホスト /mcp: [%{http_code}]\n' "$GATE_MCP/mcp" -d '{}'
curl -s -o /dev/null -w '認可サーバーのホスト メタデータ: [%{http_code}]\n' "$GATE_AS/.well-known/oauth-authorization-server"

echo "--- nginx 越しの /authorize（誰でも通すので 200）"
curl -s -w '[%{http_code}]\n' "$GATE_AS/authorize?client_id=curl"
