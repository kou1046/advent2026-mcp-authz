package main

import (
	"fmt"
	"net/http"
	"strings"
)

// metadataPrefix は Protected Resource Metadata（RFC 9728）の置き場所。
// MCP のパスが /mcp なら /.well-known/oauth-protected-resource/mcp にも置く（パスを差し込む形）。
const metadataPrefix = "/.well-known/oauth-protected-resource"

// serveMetadata は「このリソースを守っている認可サーバーはここ」というメタデータを返す。
// 認可サーバーは別のホストにいるので、起動時に渡された URL をそのまま書く。
func (s *server) serveMetadata(w http.ResponseWriter, r *http.Request) int {
	return writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 baseURL(r) + s.path,
		"authorization_servers":    []string{s.authServer},
		"bearer_methods_supported": []string{"header"},
	})
}

// unauthorized は 401 と、メタデータの場所を書いた WWW-Authenticate を返す。
// まだトークンを検証しないので、Bearer が付いていても受け入れない。
func (s *server) unauthorized(w http.ResponseWriter, r *http.Request) int {
	v := fmt.Sprintf(`Bearer resource_metadata="%s%s%s"`, baseURL(r), metadataPrefix, s.path)
	if hasBearer(r) {
		v += `, error="invalid_token"`
	}
	w.Header().Set("WWW-Authenticate", v)
	return writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
}

// authorized は、リクエストを通してよいかを決める。
// トークンを検証する仕組みがまだないので、今は誰も通さない。
func (s *server) authorized(r *http.Request) bool {
	return false
}

func hasBearer(r *http.Request) bool {
	return strings.HasPrefix(strings.ToLower(r.Header.Get("Authorization")), "bearer ")
}

// baseURL はトンネル越しでも外から見える URL を組み立てる。
func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
