package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
)

// metadataPrefix は Protected Resource Metadata（RFC 9728）の置き場所。
// MCP のパスが /mcp なら /.well-known/oauth-protected-resource/mcp に置く（パスを差し込む形）。
const metadataPrefix = "/.well-known/oauth-protected-resource"

// serveMetadata は「このリソースを守っている認可サーバーはここ」というメタデータを返す。
// 12/8 から、ホストごとに別のメタデータを返す。authorization_servers には、そのホストで信じる認可サーバーだけを書く。
func (s *server) serveMetadata(w http.ResponseWriter, r *http.Request, rt *route) int {
	return writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 baseURL(r) + rt.path,
		"authorization_servers":    []string{rt.issuer},
		"bearer_methods_supported": []string{"header"},
	})
}

// errNoToken は、そもそもトークンが付いていないこと。
var errNoToken = errors.New("no token")

// unauthorized は 401 と、メタデータの場所を書いた WWW-Authenticate を返す。
// トークンが付いていてダメだったときは、RFC 6750 の error="invalid_token" と、理由（error_description）も付ける。
// トークンが付いていないときは、error を付けない（RFC 6750 §3.1）。
func (s *server) unauthorized(w http.ResponseWriter, r *http.Request, rt *route, err error) int {
	v := fmt.Sprintf(`Bearer resource_metadata="%s%s%s"`, baseURL(r), metadataPrefix, rt.path)
	if !errors.Is(err, errNoToken) {
		v += fmt.Sprintf(`, error="invalid_token", error_description="%s"`, strings.ReplaceAll(err.Error(), `"`, `'`))
	}
	w.Header().Set("WWW-Authenticate", v)
	return writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
}

// authorize は、リクエストを通してよいかを決める。トークンの署名・iss・aud・exp を確かめる。
// 12/8 から、ホストごとに、信じる認可サーバー（iss）も、自分の URL（aud）も違う。
// テナント A のトークンを、テナント B のホストに持ってきても通らない。
func (s *server) authorize(r *http.Request, rt *route) (map[string]any, error) {
	if !hasBearer(r) {
		return nil, errNoToken
	}
	token := r.Header.Get("Authorization")[len("bearer "):]
	// ここで読む iss はまだ確かめていない値なので、よその認可サーバーの鍵を取りに行かないために使うだけ（verify がもう一度突き合わせる）。
	if iss, _ := peekClaims(token)["iss"].(string); iss != rt.issuer {
		return nil, fmt.Errorf("iss %q is not trusted on %s", iss, r.Host)
	}
	return rt.verifier.verify(token, baseURL(r)+rt.path)
}

// organizations は、トークンの organization クレームから、所属している組織（テナント）の名前を取り出す。
// Keycloak は、ふつうは名前の配列で入れる。マッパーの設定によっては、名前をキーにしたオブジェクトになる。
func organizations(v any) []string {
	var orgs []string
	switch o := v.(type) {
	case string:
		orgs = append(orgs, o)
	case []any:
		for _, x := range o {
			if s, ok := x.(string); ok {
				orgs = append(orgs, s)
			}
		}
	case map[string]any:
		for k := range o {
			orgs = append(orgs, k)
		}
	}
	slices.Sort(orgs)
	return orgs
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
	for _, k := range []string{"iss", "aud", "sub", "azp", "scope", "organization", "exp", "iat", "preferred_username"} {
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
