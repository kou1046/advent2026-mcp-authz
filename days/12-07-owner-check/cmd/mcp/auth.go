package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
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
		"authorization_servers":    s.authServers,
		"bearer_methods_supported": []string{"header"},
	})
}

// errNoToken は、そもそもトークンが付いていないこと。
var errNoToken = errors.New("no token")

// unauthorized は 401 と、メタデータの場所を書いた WWW-Authenticate を返す。
// トークンが付いていてダメだったときは、RFC 6750 の error="invalid_token" と、理由（error_description）も付ける。
// トークンが付いていないときは、error を付けない（RFC 6750 §3.1）。
func (s *server) unauthorized(w http.ResponseWriter, r *http.Request, err error) int {
	v := fmt.Sprintf(`Bearer resource_metadata="%s%s%s"`, baseURL(r), metadataPrefix, s.path)
	if !errors.Is(err, errNoToken) {
		v += fmt.Sprintf(`, error="invalid_token", error_description="%s"`, strings.ReplaceAll(err.Error(), `"`, `'`))
	}
	w.Header().Set("WWW-Authenticate", v)
	return writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
}

// authorize は、リクエストを通してよいかを決める。
// 12/6 から、トークンの署名・iss・aud・exp を確かめる。
// aud は、ふつうは自分の URL。-audience を渡すと、その値を自分だとみなす（aud が違うときの claude.ai を見る実験用）。
func (s *server) authorize(r *http.Request) (map[string]any, error) {
	if !hasBearer(r) {
		return nil, errNoToken
	}
	audience := baseURL(r) + s.path
	if s.audience != "" {
		audience = s.audience
	}
	token := r.Header.Get("Authorization")[len("bearer "):]
	// 12/7 から、信じる認可サーバーが複数ある。トークンの iss を見て、その認可サーバーの鍵で確かめる。
	// ここで読む iss はまだ確かめていない値なので、どの鍵を使うかを選ぶだけに使う（verify がもう一度突き合わせる）。
	iss, _ := peekClaims(token)["iss"].(string)
	v, ok := s.verifiers[iss]
	if !ok {
		return nil, fmt.Errorf("iss %q is not trusted", iss)
	}
	return v.verify(token, audience)
}

// peekClaims は、JWT のペイロードを署名を確かめずに読む。JWT でなければ空を返す。
func peekClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return map[string]any{}
	}
	var claims map[string]any
	if decodePart(parts[1], &claims) != nil || claims == nil {
		return map[string]any{}
	}
	return claims
}

func hasBearer(r *http.Request) bool {
	return strings.HasPrefix(strings.ToLower(r.Header.Get("Authorization")), "bearer ")
}

// tokenClaims は、ログに出すために、JWT の中身（ペイロード）を署名を確かめずに読む。
// 断ったトークンの中身も見たいので、検証とは別に読む。
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
