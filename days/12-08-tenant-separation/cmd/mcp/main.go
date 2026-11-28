// mcp は SDK を使わずに書いた、最低限の MCP サーバー（Streamable HTTP、ステートレス）。
// ツールは read_note と write_note の2つ。
//
// 12/3 から、トークンのないリクエストには 401 を返し、「認可が要る」と伝える。
// どこの認可サーバーに行けばいいかは、Protected Resource Metadata（RFC 9728）で教える。
// 12/5 から、届いたトークンの中身（iss、aud、sub など）をログに出す。
// 12/6 から、トークンの署名・iss・aud・exp を確かめ、ダメなら 401（error="invalid_token"）を返す。
// 12/7 から、メモを Postgres に置き、持ち主（トークンの iss と sub）で絞って、自分のメモだけ読み書きさせる。
// 12/8 から、テナントでも絞る。テナントの選ばせ方を2つ、ホスト名で出し分ける（パスはどちらも /mcp）。
//
//	-tenant-hosts のホスト  やり方1：URL で選ばせる。認可サーバーはテナントごとの realm。テナントはホスト名で決まる
//	それ以外のホスト        やり方2：ログイン画面で選ばせる。認可サーバーは共有の realm。テナントはトークンの organization クレームで決まる
//
// はじめはパス（/t/{テナント}/mcp）で分けたが、claude.ai ではログインまで進んだあと繋がらなかった。
// Claude のドキュメントの例（顧客ごとの URL）もサブドメインで分けていたので、ホスト名で分ける。
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
	"context"
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

	"github.com/kou1046/advent2026-mcp-authz/days/12-08-tenant-separation/internal/notes"
)

const maxRequest = 1 << 20

// mcpPath は MCP のパス。やり方1 も やり方2 も同じ。
const mcpPath = "/mcp"

// route は、MCP のホスト1つぶんの設定。ホストごとに、信じる認可サーバーとテナントの決め方が違う。
type route struct {
	path     string
	issuer   string // このホストで信じる認可サーバー。メタデータにも、これだけを書く
	verifier *verifier
	tenant   string // やり方1：ホスト名で決まるテナント。やり方2 では空（トークンの organization クレームで決める）
}

type server struct {
	tenants map[string]*route // やり方1：ホスト名ごと
	shared  *route            // やり方2：それ以外のホスト
	origins map[string]bool
	brk     string
	store   notes.Store
	logger  *slog.Logger
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "listen address")
	origins := flag.String("allowed-origins", "", "comma separated Origin values to accept (requests without Origin are always accepted)")
	authServer := flag.String("authorization-server", "", "base URL of the authorization server (Keycloak) as seen from outside; issuers are <base>/realms/<realm> (required)")
	authServerInternal := flag.String("authorization-server-internal", "", "base URL to read the authorization server's metadata from (defaults to -authorization-server)")
	tenantHosts := flag.String("tenant-hosts", "", "comma separated <realm>=<host> pairs, one per tenant; requests to that host use that tenant's realm")
	sharedRealm := flag.String("shared-realm", "", "realm shared by all tenants (Organizations); used for any other host")
	dbURL := flag.String("database-url", "", "Postgres connection string (required)")
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
	if *authServerInternal == "" {
		*authServerInternal = *authServer
	}
	tenants := splitList(*tenantHosts)
	if len(tenants) == 0 && *sharedRealm == "" {
		fmt.Fprintln(os.Stderr, "-tenant-hosts or -shared-realm is required")
		os.Exit(2)
	}
	if *dbURL == "" {
		fmt.Fprintln(os.Stderr, "-database-url is required")
		os.Exit(2)
	}
	store, err := notes.NewPostgres(context.Background(), *dbURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot connect to the database:", err)
		os.Exit(1)
	}

	s := &server{
		tenants: map[string]*route{},
		origins: map[string]bool{},
		brk:     *brk,
		store:   store,
		logger:  slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}
	newRoute := func(host, realm, tenant string) *route {
		issuer := strings.TrimRight(*authServer, "/") + "/realms/" + realm
		metadata := strings.TrimRight(*authServerInternal, "/") + "/realms/" + realm + "/.well-known/openid-configuration"
		s.logger.Info("route", "host", host, "authorization_server", issuer, "tenant", tenant)
		return &route{path: mcpPath, issuer: issuer, verifier: newVerifier(issuer, metadata), tenant: tenant}
	}
	for _, t := range tenants {
		realm, host, ok := strings.Cut(t, "=")
		if !ok || realm == "" || host == "" {
			fmt.Fprintf(os.Stderr, "-tenant-hosts must be <realm>=<host>: %s\n", t)
			os.Exit(2)
		}
		s.tenants[host] = newRoute(host, realm, realm)
	}
	if *sharedRealm != "" {
		s.shared = newRoute("*", *sharedRealm, "")
	}
	for _, o := range splitList(*origins) {
		s.origins[o] = true
	}

	s.logger.Info("listening", "addr", *addr, "break", *brk)
	if err := http.ListenAndServe(*addr, s); err != nil {
		s.logger.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

// splitList はカンマ区切りの値を、空白を除いて分ける。
func splitList(s string) []string {
	var out []string
	for _, x := range strings.Split(s, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

// logEntry はリクエストごとに1行出すログの中身。
type logEntry struct {
	era        string
	rpcMethod  string
	rpcID      string
	tool       string
	tenant     string // ツールが、どのテナントとして読み書きしたか
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
		"tenant", le.tenant,
		"client_info", le.clientInfo,
		"bearer", hasBearer(r),
		"token", tokenClaims(r),
		"auth_error", le.authError,
		"status", status,
		"duration_ms", time.Since(start).Milliseconds(),
	)
}

func (s *server) serve(w http.ResponseWriter, r *http.Request, le *logEntry) int {
	// 来たホスト名で、やり方1（テナントごとのホスト）か やり方2 かを決める。
	rt := s.routeFor(r)
	if rt == nil {
		http.NotFound(w, r)
		return http.StatusNotFound
	}
	// メタデータは、/.well-known/oauth-protected-resource に MCP のパスを差し込んだ場所に置く。
	if r.URL.Path == metadataPrefix+rt.path {
		return s.serveMetadata(w, r, rt)
	}
	if r.URL.Path != rt.path {
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
	claims, err := s.authorize(r, rt)
	if err != nil {
		if !errors.Is(err, errNoToken) {
			le.authError = err.Error()
		}
		return s.unauthorized(w, r, rt, err)
	}
	// 検証を通ったトークンから、メモの持ち主（iss と sub）と、テナントを決める材料を取り出して、ツールに渡す。
	iss, _ := claims["iss"].(string)
	sub, _ := claims["sub"].(string)
	r = r.WithContext(withCaller(r.Context(), caller{
		owner:  notes.Owner{Iss: iss, Sub: sub},
		route:  rt,
		orgs:   organizations(claims["organization"]),
		tenant: &le.tenant,
	}))

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

// routeFor は、来たホスト名の設定を返す。テナントのホストでなければ、やり方2 の設定を返す。
func (s *server) routeFor(r *http.Request) *route {
	if rt := s.tenants[r.Host]; rt != nil {
		return rt
	}
	return s.shared
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
