package main

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

// verifier は、届いたアクセストークン（JWT）を確かめる。
// 確かめるのは、署名・iss・aud・exp の4つ。
//
// 公開鍵は、認可サーバーのメタデータの jwks_uri から取ってきて、kid ごとにキャッシュする。
// 知らない kid のトークンが来たら、鍵が入れ替わったとみなして取り直す（ただし、間を空ける）。
type verifier struct {
	issuer   string // 信じる認可サーバー（iss と突き合わせる）
	metadata string // 認可サーバーのメタデータの URL（コンテナの中から読む）
	client   *http.Client

	mu        sync.Mutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
}

// refetchInterval は、知らない kid が来ても、これより短い間隔では JWKS を取り直さない。
// でたらめな kid のトークンを大量に送られて、認可サーバーを叩き続けないため。
const refetchInterval = 30 * time.Second

func newVerifier(issuer, metadata string) *verifier {
	return &verifier{
		issuer:   issuer,
		metadata: metadata,
		client:   &http.Client{Timeout: 5 * time.Second},
		keys:     map[string]*rsa.PublicKey{},
	}
}

// verify は、トークンを確かめて、中身（クレーム）を返す。
// ダメなときは、error_description にそのまま書ける理由を返す。
func (v *verifier) verify(token, audience string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("not a JWT")
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := decodePart(parts[0], &header); err != nil {
		return nil, errors.New("malformed header")
	}

	// 1. 署名。alg はトークンを送ってきた側が書いた値なので、信じずに RS256 だけを受け付ける。
	if header.Alg != "RS256" {
		return nil, fmt.Errorf("alg %q is not accepted", header.Alg)
	}
	key, err := v.key(header.Kid)
	if err != nil {
		return nil, err
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, errors.New("malformed signature")
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], sig) != nil {
		return nil, errors.New("bad signature")
	}

	// ここから先は、署名が正しいので、中身を信じて読める。
	var claims map[string]any
	if err := decodePart(parts[1], &claims); err != nil {
		return nil, errors.New("malformed payload")
	}

	// 2. iss。自分が信じる認可サーバーが出したか。
	if iss, _ := claims["iss"].(string); iss != v.issuer {
		return claims, fmt.Errorf("iss %q is not trusted", iss)
	}

	// 3. aud。自分宛てか。aud は文字列のことも、配列のこともある。
	if !hasAudience(claims["aud"], audience) {
		return claims, fmt.Errorf("aud does not include %s", audience)
	}

	// 4. exp。期限が切れていないか。
	exp, ok := claims["exp"].(float64)
	if !ok {
		return claims, errors.New("no exp")
	}
	if time.Now().After(time.Unix(int64(exp), 0)) {
		return claims, errors.New("token expired")
	}
	return claims, nil
}

func hasAudience(aud any, want string) bool {
	switch a := aud.(type) {
	case string:
		return a == want
	case []any:
		for _, x := range a {
			if s, _ := x.(string); s == want {
				return true
			}
		}
	}
	return false
}

// key は、kid に合う公開鍵を返す。手元になければ JWKS を取り直す。
func (v *verifier) key(kid string) (*rsa.PublicKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if k, ok := v.keys[kid]; ok {
		return k, nil
	}
	if time.Since(v.fetchedAt) < refetchInterval {
		return nil, fmt.Errorf("unknown kid %q", kid)
	}
	keys, err := v.fetchKeys()
	v.fetchedAt = time.Now()
	if err != nil {
		return nil, fmt.Errorf("cannot fetch JWKS: %v", err)
	}
	v.keys = keys
	if k, ok := v.keys[kid]; ok {
		return k, nil
	}
	return nil, fmt.Errorf("unknown kid %q", kid)
}

// fetchKeys は、メタデータで jwks_uri を知り、JWKS から署名用の RSA 鍵を取り出す。
func (v *verifier) fetchKeys() (map[string]*rsa.PublicKey, error) {
	var meta struct {
		Issuer  string `json:"issuer"`
		JWKSURI string `json:"jwks_uri"`
	}
	if err := v.getJSON(v.metadata, &meta); err != nil {
		return nil, err
	}
	// メタデータが、信じている認可サーバーのものかを確かめる。
	if meta.Issuer != v.issuer {
		return nil, fmt.Errorf("metadata issuer %q is not %q", meta.Issuer, v.issuer)
	}

	var jwks struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			Use string `json:"use"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := v.getJSON(meta.JWKSURI, &jwks); err != nil {
		return nil, err
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range jwks.Keys {
		// 暗号化用（use: enc）の鍵も並んでいるので、署名用の RSA 鍵だけを使う。
		if k.Kty != "RSA" || (k.Use != "" && k.Use != "sig") {
			continue
		}
		n, err1 := base64.RawURLEncoding.DecodeString(k.N)
		e, err2 := base64.RawURLEncoding.DecodeString(k.E)
		if err1 != nil || err2 != nil {
			continue
		}
		keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
	}
	return keys, nil
}

func (v *verifier) getJSON(url string, out any) error {
	resp, err := v.client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func decodePart(s string, out any) error {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}
