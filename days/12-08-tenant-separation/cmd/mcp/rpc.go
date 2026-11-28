package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
)

const (
	modernVersion = "2026-07-28"
	metaVersion   = "io.modelcontextprotocol/protocolVersion"
	metaClientCap = "io.modelcontextprotocol/clientCapabilities"
	metaServer    = "io.modelcontextprotocol/serverInfo"
)

// initialize から始める版。古いクライアントが送ってきた版がここになければ、いちばん新しいものを返す。
var legacyVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26"}

var serverInfo = map[string]any{"name": "advent-notes", "version": "0.1.0"}

const instructions = "メモを読み書きするサーバーです。read_note で一覧か1件を読み、write_note で新しいメモを書きます。"

// JSON-RPC のエラーコード。-320xx は MCP 2026-07-28 で決められたもの。
const (
	codeParseError         = -32700
	codeInvalidRequest     = -32600
	codeMethodNotFound     = -32601
	codeInvalidParams      = -32602
	codeHeaderMismatch     = -32020
	codeUnsupportedVersion = -32022
)

type rpcError struct {
	status  int
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	params  params
}

type params struct {
	Meta      map[string]json.RawMessage `json:"_meta"`
	Name      string                     `json:"name"`
	Arguments json.RawMessage            `json:"arguments"`
	// 古い版の initialize だけが使う。
	ProtocolVersion string          `json:"protocolVersion"`
	ClientInfo      json.RawMessage `json:"clientInfo"`
}

func (p params) protocolVersion() string {
	var v string
	json.Unmarshal(p.Meta[metaVersion], &v)
	return v
}

func parseRequest(body []byte) (*request, *rpcError) {
	if bytes.HasPrefix(bytes.TrimSpace(body), []byte("[")) {
		return nil, &rpcError{status: http.StatusBadRequest, Code: codeInvalidRequest, Message: "batch is not supported"}
	}
	var req request
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, &rpcError{status: http.StatusBadRequest, Code: codeParseError, Message: "invalid JSON"}
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		return nil, &rpcError{status: http.StatusBadRequest, Code: codeInvalidRequest, Message: "not a JSON-RPC 2.0 request"}
	}
	if string(req.ID) == "null" {
		req.ID = nil
	}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &req.params); err != nil {
			return nil, &rpcError{status: http.StatusBadRequest, Code: codeInvalidParams, Message: "invalid params"}
		}
	}
	return &req, nil
}

// handleModern は 2026-07-28 のリクエストを処理する。前のリクエストに頼らず、毎回ここで全部確かめる。
func (s *server) handleModern(r *http.Request, req *request) (map[string]any, *rpcError) {
	bad := func(code int, msg string, data any) *rpcError {
		return &rpcError{status: http.StatusBadRequest, Code: code, Message: msg, Data: data}
	}

	v := req.params.protocolVersion()
	if v != modernVersion {
		return nil, bad(codeUnsupportedVersion, "Unsupported protocol version",
			map[string]any{"supported": s.supportedVersions(), "requested": v})
	}
	if _, ok := req.params.Meta[metaClientCap]; !ok {
		return nil, bad(codeInvalidParams, "missing "+metaClientCap, nil)
	}

	// 本文を読まずに振り分ける proxy のために、同じ値がヘッダーにも載っている。食い違いは断る。
	if h := r.Header.Get("MCP-Protocol-Version"); h != v {
		return nil, bad(codeHeaderMismatch, "MCP-Protocol-Version header does not match _meta", nil)
	}
	if h := r.Header.Get("Mcp-Method"); h != req.Method {
		return nil, bad(codeHeaderMismatch, "Mcp-Method header does not match method", nil)
	}
	if req.Method == "tools/call" {
		if h := decodeHeader(r.Header.Get("Mcp-Name")); h != req.params.Name {
			return nil, bad(codeHeaderMismatch, "Mcp-Name header does not match params.name", nil)
		}
	}

	var result map[string]any
	var rerr *rpcError
	switch req.Method {
	case "server/discover":
		if s.brk == "no-discover" {
			return nil, &rpcError{status: http.StatusNotFound, Code: codeMethodNotFound, Message: "Method not found: " + req.Method}
		}
		result = map[string]any{
			"supportedVersions": s.supportedVersions(),
			"capabilities":      map[string]any{"tools": map[string]any{}},
			"instructions":      instructions,
			"ttlMs":             3600000,
			"cacheScope":        "public",
		}
	case "tools/list":
		result = map[string]any{"tools": s.toolDefs(routeFrom(r.Context())), "ttlMs": 300000, "cacheScope": "public"}
	case "tools/call":
		result, rerr = s.callTool(r.Context(), req.params)
	default:
		return nil, &rpcError{status: http.StatusNotFound, Code: codeMethodNotFound, Message: "Method not found: " + req.Method}
	}
	if rerr != nil {
		if rerr.status == 0 {
			rerr.status = http.StatusBadRequest
		}
		return nil, rerr
	}
	result["resultType"] = "complete"
	result["_meta"] = map[string]any{metaServer: serverInfo}
	return result, nil
}

// handleLegacy は initialize から始める 2025-11-25 以前のリクエストを処理する。
// セッション ID は発行しないので、こちらもステートレスに動く。
func (s *server) handleLegacy(r *http.Request, req *request) (map[string]any, *rpcError) {
	if h := r.Header.Get("MCP-Protocol-Version"); h != "" && !slices.Contains(legacyVersions, h) {
		return nil, &rpcError{status: http.StatusBadRequest, Code: codeInvalidRequest, Message: "unsupported MCP-Protocol-Version: " + h}
	}

	switch req.Method {
	case "initialize":
		if s.brk == "no-initialize" {
			break
		}
		v := req.params.ProtocolVersion
		if !slices.Contains(legacyVersions, v) {
			v = legacyVersions[0]
		}
		if s.brk == "bad-version" {
			v = "1900-01-01"
		}
		return map[string]any{
			"protocolVersion": v,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      serverInfo,
			"instructions":    instructions,
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": s.toolDefs(routeFrom(r.Context()))}, nil
	case "tools/call":
		return s.callTool(r.Context(), req.params)
	}
	return nil, &rpcError{Code: codeMethodNotFound, Message: "Method not found: " + req.Method}
}

func (s *server) supportedVersions() []string {
	if s.brk == "bad-version" {
		return []string{"1900-01-01"}
	}
	return []string{modernVersion}
}

// decodeHeader はヘッダーに載せられない文字を含む値の =?base64?…?= 形式をほどく。
func decodeHeader(v string) string {
	if !strings.HasPrefix(v, "=?base64?") || !strings.HasSuffix(v, "?=") {
		return v
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(strings.TrimPrefix(v, "=?base64?"), "?="))
	if err != nil {
		return v
	}
	return string(b)
}
