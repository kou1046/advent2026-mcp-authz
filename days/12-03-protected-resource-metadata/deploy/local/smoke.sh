#!/bin/sh
# 立ち上げた MCP サーバーを curl で一通り叩く。新しい版（2026-07-28）と古い版（initialize から）の両方を試す。
set -eu

MCP=${MCP:-http://127.0.0.1:8080/mcp}
GATE=${GATE:-http://127.0.0.1:8081/mcp}
META='"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}'

call() { # 見出し メソッド ツール名 本文
	echo "--- $1"
	curl -s -w ' [%{http_code}]\n' "$MCP" \
		-H 'Content-Type: application/json' \
		-H 'Accept: application/json, text/event-stream' \
		-H 'MCP-Protocol-Version: 2026-07-28' \
		-H "Mcp-Method: $2" ${3:+-H "Mcp-Name: $3"} \
		-d "$4"
}

call "server/discover" server/discover "" "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"server/discover\",\"params\":{$META}}"
call "tools/list" tools/list "" "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/list\",\"params\":{$META}}"
call "write_note" tools/call write_note "{\"jsonrpc\":\"2.0\",\"id\":3,\"method\":\"tools/call\",\"params\":{\"name\":\"write_note\",\"arguments\":{\"title\":\"hello\",\"body\":\"はじめてのメモ\"},$META}}"
call "read_note（一覧）" tools/call read_note "{\"jsonrpc\":\"2.0\",\"id\":4,\"method\":\"tools/call\",\"params\":{\"name\":\"read_note\",\"arguments\":{},$META}}"

echo "--- 古い版の initialize"
curl -s -w ' [%{http_code}]\n' "$MCP" -H 'Content-Type: application/json' \
	-d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"curl","version":"0"}}}'

echo "--- nginx 越し（claude.ai の範囲外なので 403 のはず）"
curl -s -o /dev/null -w '[%{http_code}]\n' "$GATE" -d '{}'
