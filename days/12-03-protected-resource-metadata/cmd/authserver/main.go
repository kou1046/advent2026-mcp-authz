// authserver は 12/3 で使う、仮の認可サーバー。
// 認可サーバーのメタデータ（RFC 8414）で登録・認可の入口を広告し、届いたリクエストを JSON Lines でログに出す。
// ログイン画面はなく、/authorize に来たら「ここまで来ました」とだけ返す。本物の認可サーバーは 12/4 で立てる。
//
// MCP サーバーとは別のホストに置くので、MCP サーバーのメタデータ（RFC 9728）は返さない。
//
//	-dcr=false   メタデータから registration_endpoint を外し、/register も受け付けない
//	-cimd=false  メタデータから client_id_metadata_document_supported を外す
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
	dcr  bool
	cimd bool
}

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	var cfg config
	flag.BoolVar(&cfg.dcr, "dcr", true, "advertise registration_endpoint (DCR)")
	flag.BoolVar(&cfg.cimd, "cimd", true, "advertise client_id_metadata_document_supported (CIMD)")
	flag.Parse()

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

	logger.Info("listening", "addr", *addr, "dcr", cfg.dcr, "cimd", cfg.cimd)
	srv := &http.Server{Addr: *addr, Handler: h, ReadHeaderTimeout: 10 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		logger.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

func respond(w http.ResponseWriter, r *http.Request, cfg config, body []byte) int {
	switch path := r.URL.Path; {
	case strings.HasPrefix(path, "/.well-known/oauth-authorization-server"),
		strings.HasPrefix(path, "/.well-known/openid-configuration"):
		return writeJSON(w, http.StatusOK, metadata(baseURL(r), cfg))
	case path == "/register" && cfg.dcr:
		return register(w, body)
	case path == "/authorize":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "authserver: /authorize に到達しました。ここから先は実装していません。")
		return http.StatusOK
	case path == "/token":
		return writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
	}
	http.NotFound(w, r)
	return http.StatusNotFound
}

// metadata は RFC 8414 の認可サーバーメタデータ。中身は登録・認可の入口を広告するためだけのもの。
func metadata(base string, cfg config) map[string]any {
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

// register は RFC 7591 の登録応答を返す。送られてきたメタデータに client_id を付けて返すだけで、何も保存しない。
func register(w http.ResponseWriter, body []byte) int {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_client_metadata"})
	}
	id := make([]byte, 8)
	rand.Read(id)
	req["client_id"] = "authserver-" + hex.EncodeToString(id)
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
