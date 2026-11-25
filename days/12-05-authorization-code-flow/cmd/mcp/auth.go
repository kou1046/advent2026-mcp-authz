package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// metadataPrefix は Protected Resource Metadata（RFC 9728）の置き場所。
// MCP のパスが /mcp なら /.well-known/oauth-protected-resource/mcp にも置く（パスを差し込む形）。
const metadataPrefix = "/.well-known/oauth-protected-resource"

// serveMetadata は「このリソースを守っている認可サーバーはここ」というメタデータを返す。
// 認可サーバーは別のホストにいるので、起動時に渡された URL をそのまま書く。
// resource は、ふつうは自分の URL。-resource を渡すと、その値を名乗る（宛先を偽る実験用）。
func (s *server) serveMetadata(w http.ResponseWriter, r *http.Request) int {
	resource := baseURL(r) + s.path
	if s.resource != "" {
		resource = s.resource
	}
	return writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 resource,
		"authorization_servers":    []string{s.authServer},
		"bearer_methods_supported": []string{"header"},
	})
}

// unauthorized は 401 と、メタデータの場所を書いた WWW-Authenticate を返す。
func (s *server) unauthorized(w http.ResponseWriter, r *http.Request) int {
	v := fmt.Sprintf(`Bearer resource_metadata="%s%s%s"`, baseURL(r), metadataPrefix, s.path)
	w.Header().Set("WWW-Authenticate", v)
	return writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
}

// authorized は、リクエストを通してよいかを決める。
// 12/5 では、Bearer が付いていれば中身を確かめずに通す。どんな文字列でも通ってしまう。
// 署名や aud を確かめるのは 12/6。
func (s *server) authorized(r *http.Request) bool {
	return hasBearer(r)
}

func hasBearer(r *http.Request) bool {
	return strings.HasPrefix(strings.ToLower(r.Header.Get("Authorization")), "bearer ")
}

// tokenClaims は、ログに出すために、JWT の中身（ペイロード）を署名を確かめずに読む。
// トークンそのものはログに出さない。JWT でなければ nil を返す。
func tokenClaims(r *http.Request) map[string]any {
	if !hasBearer(r) {
		return nil
	}
	parts := strings.Split(r.Header.Get("Authorization")[len("bearer "):], ".")
	if len(parts) != 3 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var all map[string]any
	if json.Unmarshal(payload, &all) != nil {
		return nil
	}
	claims := map[string]any{}
	for _, k := range []string{"iss", "aud", "sub", "azp", "scope", "exp", "iat", "preferred_username"} {
		if v, ok := all[k]; ok {
			claims[k] = v
		}
	}
	return claims
}

// baseURL はトンネル越しでも外から見える URL を組み立てる。
func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
