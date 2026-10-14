package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	defaultAccount  = "ucp-cli-auth@clsd-ucp.iam.service-accounts.roc"
	defaultProxyURL = "https://roc-tenant-sa-kc-jws-80-keycloak.jpe2-caas1-beta1.caas.jpe2z.r-local.net/token"
	defaultLBaaSURL = "https://qa2-lbaas.r-local.net"
	assertorAud     = "rns:roc:lbaas"
	assertorTTL     = 20 * time.Second
	httpTimeout     = 20 * time.Second
	maxBodyPrint    = 400
)

var b64 = base64.RawURLEncoding

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func client(insecure bool) *http.Client {
	return &http.Client{
		Timeout:   httpTimeout,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: insecure}},
	}
}

func signAssertor(jwkPath, account string, auds []string, audArray bool) (string, error) {
	raw, err := os.ReadFile(jwkPath)
	if err != nil {
		return "", err
	}
	var k struct{ Kid, D, Crv, Kty string }
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", err
	}
	k.Kid, k.D, k.Crv, k.Kty = m["kid"], m["d"], m["crv"], m["kty"]
	if k.Kty != "OKP" || k.Crv != "Ed25519" {
		return "", fmt.Errorf("unsupported JWK kty=%s crv=%s", k.Kty, k.Crv)
	}
	seed, err := b64.DecodeString(k.D)
	if err != nil {
		return "", err
	}
	priv := ed25519.NewKeyFromSeed(seed)

	jti := make([]byte, 16)
	_, _ = rand.Read(jti)
	now := time.Now()
	header := map[string]any{"alg": "EdDSA", "kid": k.Kid}
	claims := map[string]any{
		"iss": account, "sub": account, "aud": assertorAud,
		"iat": now.Unix(), "exp": now.Add(assertorTTL).Unix(),
		"jti": fmt.Sprintf("%x", jti),
	}
	if audArray || len(auds) > 1 {
		claims["aud"] = auds
	} else {
		claims["aud"] = auds[0]
	}
	h, _ := json.Marshal(header)
	c, _ := json.Marshal(claims)
	signing := b64.EncodeToString(h) + "." + b64.EncodeToString(c)
	return signing + "." + b64.EncodeToString(ed25519.Sign(priv, []byte(signing))), nil
}

func decodeJWT(tok string) (header, claims map[string]any, err error) {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return nil, nil, fmt.Errorf("not a JWT")
	}
	for i, dst := range []*map[string]any{&header, &claims} {
		b, e := b64.DecodeString(parts[i])
		if e != nil {
			return nil, nil, e
		}
		if e := json.Unmarshal(b, dst); e != nil {
			return nil, nil, e
		}
	}
	return header, claims, nil
}

func trunc(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxBodyPrint {
		return s[:maxBodyPrint] + "..."
	}
	return s
}

func exchange(c *http.Client, proxyURL, assertion, scope string) (string, error) {
	form := url.Values{
		"grant_type":            {"client_credentials"},
		"client_assertion_type": {"urn:ietf:params:oauth:client-assertion-type:jwt-bearer"},
		"client_assertion":      {assertion},
	}
	if scope != "" {
		form.Set("scope", scope)
	}
	resp, err := c.PostForm(proxyURL, form)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("  status: %d\n", resp.StatusCode)
	if resp.StatusCode != http.StatusOK {
		fmt.Printf("  body:   %s\n", trunc(string(body)))
		return "", fmt.Errorf("proxy returned %d", resp.StatusCode)
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		TokenType   string `json:"token_type"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	fmt.Printf("  token_type=%s expires_in=%d\n", out.TokenType, out.ExpiresIn)
	return out.AccessToken, nil
}

func callLBaaS(c *http.Client, label, base, path, token string) {
	req, _ := http.NewRequest(http.MethodGet, base+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.Do(req)
	if err != nil {
		fmt.Printf("  [%s] GET %s -> error: %v\n", label, path, err)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("  [%s] GET %s -> %d\n", label, path, resp.StatusCode)
	fmt.Printf("      body: %s\n", trunc(string(body)))
	for _, h := range []string{"X-Request-Id", "Www-Authenticate", "Server"} {
		if v := resp.Header.Get(h); v != "" {
			fmt.Printf("      %s: %s\n", h, v)
		}
	}
}

func checkIssuer(c *http.Client, label, iss string) {
	u, err := url.Parse(iss)
	if err != nil || u.Host == "" {
		fmt.Printf("  [%s] invalid issuer %q\n", label, iss)
		return
	}
	addrs, err := net.LookupHost(u.Hostname())
	if err != nil {
		fmt.Printf("  [%s] DNS %s -> %v\n", label, u.Hostname(), err)
	} else {
		fmt.Printf("  [%s] DNS %s -> %v\n", label, u.Hostname(), addrs)
	}
	disc := strings.TrimSuffix(iss, "/") + "/.well-known/openid-configuration"
	resp, err := c.Get(disc)
	if err != nil {
		fmt.Printf("  [%s] discovery -> error: %v\n", label, err)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("  [%s] discovery %s -> %d\n", label, disc, resp.StatusCode)
	if resp.StatusCode != http.StatusOK {
		fmt.Printf("      body: %s\n", trunc(string(body)))
		return
	}
	var d struct {
		Issuer  string `json:"issuer"`
		JWKSURI string `json:"jwks_uri"`
	}
	_ = json.Unmarshal(body, &d)
	fmt.Printf("      issuer=%s jwks_uri=%s\n", d.Issuer, d.JWKSURI)
	if d.JWKSURI == "" {
		return
	}
	jr, err := c.Get(d.JWKSURI)
	if err != nil {
		fmt.Printf("      jwks -> error: %v\n", err)
		return
	}
	defer jr.Body.Close()
	fmt.Printf("      jwks -> %d\n", jr.StatusCode)
}

func main() {
	jwkPath := os.Getenv("JWK_PATH")
	if jwkPath == "" {
		must(fmt.Errorf("JWK_PATH is required"))
	}
	account := env("SA_ACCOUNT", defaultAccount)
	proxyURL := env("PROXY_TOKEN_URL", defaultProxyURL)
	lbaas := strings.TrimSuffix(env("LBAAS_BASE_URL", defaultLBaaSURL), "/")
	insecure := env("TLS_INSECURE", "true") == "true"
	c := client(insecure)
	audArray := env("ASSERTOR_AUD_ARRAY", "false") == "true"
	scope := os.Getenv("TOKEN_SCOPE")
	auds := strings.Split(env("ASSERTOR_AUD", assertorAud), ",")
	controls := env("CONTROLS", "true") == "true"
	fmt.Printf("variant: assertor_aud=%v assertor_aud_array=%v token_scope=%q\n", auds, audArray, scope)

	fmt.Println("== 1. Sign assertor")
	assertion, err := signAssertor(jwkPath, account, auds, audArray)
	must(err)
	h, cl, _ := decodeJWT(assertion)
	fmt.Printf("  header=%v\n  claims=%v\n", h, cl)

	fmt.Println("== 2. Token exchange (proxy)")
	token, err := exchange(c, proxyURL, assertion, scope)
	must(err)

	fmt.Println("== 3. Access token decode")
	th, tc, err := decodeJWT(token)
	must(err)
	fmt.Printf("  header=%v\n", th)
	for _, k := range []string{"iss", "aud", "azp", "sub", "email", "iat", "exp", "groups"} {
		fmt.Printf("  %s=%v\n", k, tc[k])
	}
	var extra []string
	for k := range tc {
		switch k {
		case "iss", "aud", "azp", "sub", "email", "iat", "exp", "groups":
		default:
			extra = append(extra, k)
		}
	}
	fmt.Printf("  other claim names: %v\n", extra)
	iss, _ := tc["iss"].(string)

	fmt.Println("== 4. Issuer checks")
	checkIssuer(c, "iss as issued", iss)
	if alt := strings.Replace(iss, "oneclould", "onecloud", 1); alt != iss {
		checkIssuer(c, "iss with 'onecloud'", alt)
	}

	fmt.Println("== 5. LBaaS call with proxy token")
	callLBaaS(c, "proxy token", lbaas, "/v2/tenants", token)

	if !controls {
		return
	}
	fmt.Println("== 6. Controls")
	callLBaaS(c, "no token", lbaas, "/v2/tenants", "")
	callLBaaS(c, "malformed token", lbaas, "/v2/tenants", "not-a-jwt")
	parts := strings.Split(token, ".")
	if len(parts) == 3 {
		badSig := parts[0] + "." + parts[1] + "." + b64.EncodeToString([]byte(strings.Repeat("x", 64)))
		callLBaaS(c, "bad signature", lbaas, "/v2/tenants", badSig)
	}
	for _, p := range []string{"/", "/v2", "/v2/health", "/healthz"} {
		callLBaaS(c, "proxy token, other path", lbaas, p, token)
	}
}
