// mcp は SDK を使わずに書いた、最低限の MCP サーバー（Streamable HTTP、ステートレス）。
// ツールは read_note と write_note の2つ。
//
// 12/3 から、トークンのないリクエストには 401 を返し、「認可が要る」と伝える。
// どこの認可サーバーに行けばいいかは、Protected Resource Metadata（RFC 9728）で教える。
// 12/5 から、届いたトークンの中身（iss、aud、sub など）をログに出す。
// -resource を渡すと、メタデータの resource でその値を名乗る（宛先を偽る実験用）。
// 12/6 から、トークンの署名・iss・aud・exp を確かめ、ダメなら 401（error="invalid_token"）を返す。
// -audience を渡すと、その値を自分の aud だとみなす（本物のトークンを、よそ宛てに見せる実験用）。
//
// MCP 仕様 2026-07-28（initialize なし、毎回 _meta でバージョンを送る）を主にしつつ、
// initialize から始める 2025-11-25 以前のクライアントにも応える。
//
//	-break=no-initialize  古い版の initialize だけに「そんなメソッドはない」と返す
//	-break=no-discover    新しい版の server/discover だけに「そんなメソッドはない」と返す
//	-break=bad-version    サーバーが対応する版として、存在しない版を返す
//	-break=bad-schema     ツールの inputSchema を null にする
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/kou1046/advent2026-mcp-authz/days/12-06-token-validation/internal/notes"
)

const maxRequest = 1 << 20

type server struct {
	path       string
	authServer string
	resource   string
	audience   string
	verifier   *verifier
	origins    map[string]bool
	brk        string
	store      notes.Store
	logger     *slog.Logger
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "listen address")
	path := flag.String("path", "/mcp", "path of the MCP endpoint")
	origins := flag.String("allowed-origins", "", "comma separated Origin values to accept (requests without Origin are always accepted)")
	authServer := flag.String("authorization-server", "", "authorization server URL advertised in the metadata (required)")
	resource := flag.String("resource", "", "resource advertised in the metadata instead of this server's own URL (to try spoofing)")
	audience := flag.String("audience", "", "aud to accept instead of this server's own URL (to make valid tokens look addressed elsewhere)")
	asMetadata := flag.String("authorization-server-metadata", "", "URL to read the authorization server metadata from (defaults to the OpenID configuration under -authorization-server)")
	brk := flag.String("break", "", "break mode: no-initialize, no-discover, bad-version or bad-schema")
	flag.Parse()

	switch *brk {
	case "", "no-initialize", "no-discover", "bad-version", "bad-schema":
	default:
		fmt.Fprintf(os.Stderr, "unknown break mode: %s\n", *brk)
		os.Exit(2)
	}
	if *authServer == "" {
		fmt.Fprintln(os.Stderr, "-authorization-server is required")
		os.Exit(2)
	}

	if *asMetadata == "" {
		*asMetadata = *authServer + "/.well-known/openid-configuration"
	}

	s := &server{
		path:       *path,
		authServer: *authServer,
		resource:   *resource,
		audience:   *audience,
		verifier:   newVerifier(*authServer, *asMetadata),
		origins:    map[string]bool{},
		brk:        *brk,
		store:      notes.NewMemory(),
		logger:     slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}
	for _, o := range strings.Split(*origins, ",") {
		if o = strings.TrimSpace(o); o != "" {
			s.origins[o] = true
		}
	}

	s.logger.Info("listening", "addr", *addr, "path", *path, "resource", *resource, "audience", *audience, "authorization_server_metadata", *asMetadata, "break", *brk)
	if err := http.ListenAndServe(*addr, s); err != nil {
		s.logger.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

// logEntry はリクエストごとに1行出すログの中身。
type logEntry struct {
	era        string
	rpcMethod  string
	rpcID      string
	tool       string
	clientInfo json.RawMessage
	authError  string
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	var le logEntry
	status := s.serve(w, r, &le)

	s.logger.Info("request",
		"client_ip", clientIP(r),
		"method", r.Method,
		"path", r.URL.Path,
		"origin", r.Header.Get("Origin"),
		"user_agent", r.UserAgent(),
		"protocol_version", r.Header.Get("MCP-Protocol-Version"),
		"era", le.era,
		"rpc_method", le.rpcMethod,
		"rpc_id", le.rpcID,
		"tool", le.tool,
		"client_info", le.clientInfo,
		"bearer", hasBearer(r),
		"token", tokenClaims(r),
		"auth_error", le.authError,
		"status", status,
		"duration_ms", time.Since(start).Milliseconds(),
	)
}

func (s *server) serve(w http.ResponseWriter, r *http.Request, le *logEntry) int {
	if p := r.URL.Path; p == metadataPrefix || p == metadataPrefix+s.path {
		return s.serveMetadata(w, r)
	}
	if r.URL.Path != s.path {
		http.NotFound(w, r)
		return http.StatusNotFound
	}
	// ブラウザ経由の DNS リバインディング対策。Origin が付いていて知らない値なら断る。
	if o := r.Header.Get("Origin"); o != "" && !s.origins[o] {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return http.StatusForbidden
	}
	// ステートレスなので、サーバーから話しかける GET のストリームも、セッションを消す DELETE も持たない。
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return http.StatusMethodNotAllowed
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequest))
	if err != nil {
		return writeError(w, nil, &rpcError{status: http.StatusBadRequest, Code: codeParseError, Message: "cannot read body"})
	}
	req, rerr := parseRequest(body)
	if rerr != nil {
		return writeError(w, nil, rerr)
	}
	le.rpcMethod = req.Method
	le.rpcID = string(req.ID)
	le.tool = req.params.Name
	le.clientInfo = req.params.ClientInfo

	// 本文を読んでから断るのは、何を呼ぼうとして断られたのかをログに残すため。
	if _, err := s.authorize(r); err != nil {
		if !errors.Is(err, errNoToken) {
			le.authError = err.Error()
		}
		return s.unauthorized(w, r, err)
	}

	// 通知には返事をしない。
	if req.ID == nil {
		w.WriteHeader(http.StatusAccepted)
		return http.StatusAccepted
	}

	var result map[string]any
	if req.params.protocolVersion() != "" {
		le.era = "modern"
		result, rerr = s.handleModern(r, req)
	} else {
		le.era = "legacy"
		result, rerr = s.handleLegacy(r, req)
	}
	if rerr != nil {
		return writeError(w, req.ID, rerr)
	}
	return writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
}

func clientIP(r *http.Request) string {
	if ip := r.Header.Get("CF-Connecting-IP"); ip != "" {
		return ip
	}
	return r.RemoteAddr
}

func writeJSON(w http.ResponseWriter, status int, v any) int {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
	return status
}

func writeError(w http.ResponseWriter, id json.RawMessage, e *rpcError) int {
	status := e.status
	if status == 0 {
		status = http.StatusOK
	}
	return writeJSON(w, status, map[string]any{"jsonrpc": "2.0", "id": id, "error": e})
}
