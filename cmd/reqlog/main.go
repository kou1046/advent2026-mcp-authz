// reqlog は届いたリクエストをすべて JSON Lines でログに出すだけのサーバー。
// Day 1 で、claude.ai などの MCP クライアントが登録時に何をどの順で叩くかを観察するために使う。
//
//	-mode=404  すべて 404 を返す（何も知らないクライアントが最初にどこを叩くか）
//	-mode=401  401 + WWW-Authenticate と Protected Resource Metadata (RFC 9728) を返す
//	           （認可サーバーのメタデータを探しに行くところまでフローを進める）
//	-mode=as   401 モードに加えて、自分自身を偽の認可サーバーとして広告する
//	           （登録・認可エンドポイントへのリダイレクトまでフローを進める）
package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

const maxBody = 64 << 10

type config struct {
	mode         string
	resourcePath string
	dcr          bool
	cimd         bool
}

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	var cfg config
	flag.StringVar(&cfg.mode, "mode", "404", "response mode: 404, 401 or as")
	flag.StringVar(&cfg.resourcePath, "resource-path", "/mcp", "path of the MCP endpoint advertised as the protected resource")
	flag.BoolVar(&cfg.dcr, "dcr", true, "as mode: advertise registration_endpoint (DCR)")
	flag.BoolVar(&cfg.cimd, "cimd", true, "as mode: advertise client_id_metadata_document_supported (CIMD)")
	flag.Parse()

	if cfg.mode != "404" && cfg.mode != "401" && cfg.mode != "as" {
		fmt.Fprintf(os.Stderr, "unknown mode: %s\n", cfg.mode)
		os.Exit(2)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	var seq atomic.Int64

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, maxBody))
		status := respond(w, r, cfg, body)

		logger.Info("request",
			"seq", seq.Add(1),
			"client_ip", clientIP(r),
			"method", r.Method,
			"host", r.Host,
			"path", r.URL.Path,
			"query", r.URL.RawQuery,
			"headers", redact(r.Header),
			"body", bodyValue(body),
			"status", status,
		)
	})

	logger.Info("listening", "addr", *addr, "mode", cfg.mode, "dcr", cfg.dcr, "cimd", cfg.cimd)
	srv := &http.Server{Addr: *addr, Handler: h, ReadHeaderTimeout: 10 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		logger.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

func respond(w http.ResponseWriter, r *http.Request, cfg config, body []byte) int {
	if cfg.mode == "404" {
		http.NotFound(w, r)
		return http.StatusNotFound
	}

	base := baseURL(r)
	path := r.URL.Path

	if strings.HasPrefix(path, "/.well-known/oauth-protected-resource") {
		return writeJSON(w, http.StatusOK, map[string]any{
			"resource":              base + cfg.resourcePath,
			"authorization_servers": []string{base},
		})
	}

	if cfg.mode == "as" {
		switch {
		case strings.HasPrefix(path, "/.well-known/oauth-authorization-server"),
			strings.HasPrefix(path, "/.well-known/openid-configuration"):
			return writeJSON(w, http.StatusOK, asMetadata(base, cfg))
		case path == "/register" && cfg.dcr:
			return register(w, body)
		case path == "/authorize":
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, "reqlog: /authorize に到達しました。ここから先は実装していません。")
			return http.StatusOK
		case path == "/token":
			return writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
		}
	}

	if !strings.HasPrefix(path, "/.well-known/") {
		w.Header().Set("WWW-Authenticate",
			fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource"`, base))
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return http.StatusUnauthorized
	}

	http.NotFound(w, r)
	return http.StatusNotFound
}

// asMetadata は RFC 8414 の認可サーバーメタデータ。中身は登録・認可の入口を広告するためだけのもの。
func asMetadata(base string, cfg config) map[string]any {
	m := map[string]any{
		"issuer":                                base,
		"authorization_endpoint":                base + "/authorize",
		"token_endpoint":                        base + "/token",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
	}
	if cfg.dcr {
		m["registration_endpoint"] = base + "/register"
	}
	if cfg.cimd {
		m["client_id_metadata_document_supported"] = true
	}
	return m
}

// register は RFC 7591 の登録応答を返す。送られてきたメタデータに client_id を付けて返すだけ。
func register(w http.ResponseWriter, body []byte) int {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_client_metadata"})
	}
	id := make([]byte, 8)
	rand.Read(id)
	req["client_id"] = "reqlog-" + hex.EncodeToString(id)
	req["client_id_issued_at"] = time.Now().Unix()
	return writeJSON(w, http.StatusCreated, req)
}

func writeJSON(w http.ResponseWriter, status int, v any) int {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
	return status
}

// baseURL はトンネル越しでも外から見える URL を組み立てる。
func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func clientIP(r *http.Request) string {
	if ip := r.Header.Get("CF-Connecting-IP"); ip != "" {
		return ip
	}
	return r.RemoteAddr
}

// redact は記事にそのまま貼れるよう、トークンや Cookie を伏せる。
func redact(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		switch strings.ToLower(k) {
		case "authorization", "cookie":
			out[k] = "[redacted]"
		default:
			out[k] = strings.Join(v, ", ")
		}
	}
	return out
}

func bodyValue(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	if json.Valid(b) {
		var buf bytes.Buffer
		json.Compact(&buf, b)
		return json.RawMessage(buf.Bytes())
	}
	return string(b)
}
