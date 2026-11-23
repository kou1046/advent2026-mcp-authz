#!/bin/sh
# 立ち上げた MCP サーバーを curl で叩く。トークンなしでは 401 になり、メタデータで認可サーバーを教えることを確かめる。
set -eu

BASE=${BASE:-http://127.0.0.1:8080}
GATE=${GATE:-http://127.0.0.1:8081/mcp}
META='"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}'

echo "--- server/discover（トークンなし → 401 と WWW-Authenticate）"
curl -s -i "$BASE/mcp" \
	-H 'Content-Type: application/json' \
	-H 'Accept: application/json, text/event-stream' \
	-H 'MCP-Protocol-Version: 2026-07-28' \
	-H 'Mcp-Method: server/discover' \
	-d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"server/discover\",\"params\":{$META}}" | grep -iE '^HTTP|^www-authenticate'

echo "--- 古い版の initialize（トークンなし → 401）"
curl -s -o /dev/null -w '[%{http_code}]\n' "$BASE/mcp" -H 'Content-Type: application/json' \
	-d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"curl","version":"0"}}}'

echo "--- Protected Resource Metadata（RFC 9728）"
curl -s -w ' [%{http_code}]\n' "$BASE/.well-known/oauth-protected-resource/mcp"

echo "--- nginx 越し（claude.ai の範囲外なので 403 のはず）"
curl -s -o /dev/null -w '[%{http_code}]\n' "$GATE" -d '{}'
