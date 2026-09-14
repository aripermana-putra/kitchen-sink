// keycloak-audience-claim inspects the aud and azp claims in an access
// token issued by the QA Keycloak realm, to determine whether enforcing
// jwt.WithAudience("ucp-platform") in the JWT middleware is feasible
// without a Keycloak configuration change.
//
// Usage:
//
//	KEYCLOAK_ISSUER=https://qa2-accounts-onecloud.rakuten-it.com/auth/realms/roc \
//	KEYCLOAK_CLIENT_ID=rns:roc:portal \
//	go run .
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const callbackPort = 18080

func main() {
	issuer := requireEnv("KEYCLOAK_ISSUER")
	clientID := requireEnv("KEYCLOAK_CLIENT_ID")

	fmt.Println("========================================")
	fmt.Println("Flow: inspect aud claim in access token")
	fmt.Println("========================================")

	fmt.Println("\n--- Step 1: Login (PKCE) ---")
	tokens, err := login(issuer, clientID)
	if err != nil {
		fatalf("login failed: %v", err)
	}

	fmt.Println("\n--- Step 2: Decode access token claims ---")
	claims := decodePayload(tokens.AccessToken)

	fmt.Printf("\n%-20s = %v\n", "iss", claims["iss"])
	fmt.Printf("%-20s = %v\n", "sub", claims["sub"])
	fmt.Printf("%-20s = %v\n", "email", claims["email"])
	fmt.Printf("%-20s = %v\n", "azp", claims["azp"])
	fmt.Printf("%-20s = %v\n", "aud", claims["aud"])

	fmt.Println("\n--- Step 3: Verdict ---")

	const ucpClientID = "ucp-platform"

	aud := claims["aud"]
	azp := claims["azp"]

	fmt.Printf("\nazp = %v\n", azp)
	fmt.Println("(azp is the client the token was issued to — always present but not enforced by jwt library by default)")

	found := audienceContains(aud, ucpClientID)
	fmt.Printf("\naud = %v\n", aud)
	if found {
		fmt.Printf("\n✅ '%s' IS present in aud\n", ucpClientID)
		fmt.Println("   → jwt.WithAudience() enforcement is feasible with no Keycloak config change.")
	} else {
		fmt.Printf("\n❌ '%s' is NOT present in aud\n", ucpClientID)
		fmt.Println("   → Enforcing audience would reject all tokens today.")
		fmt.Println("   → A Keycloak audience mapper must be added to the 'ucp-platform' client first,")
		fmt.Println("     so that tokens issued to 'ucp-cli' include 'ucp-platform' in their aud claim.")
	}

	fmt.Println("\n--- All claims (for reference) ---")
	for k, v := range claims {
		fmt.Printf("  %-24s = %v\n", k, v)
	}
}

// audienceContains reports whether the aud claim (string or []interface{})
// contains target.
func audienceContains(aud any, target string) bool {
	switch v := aud.(type) {
	case string:
		return v == target
	case []any:
		for _, a := range v {
			if a == target {
				return true
			}
		}
	}
	return false
}

// decodePayload base64-decodes the JWT payload and returns the claims as a
// map. No signature verification — this is for claim inspection only.
func decodePayload(tokenStr string) map[string]any {
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		fatalf("malformed JWT: expected 3 parts, got %d", len(parts))
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		fatalf("base64 decode: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		fatalf("json parse: %v", err)
	}
	return claims
}

// --- PKCE login (no external deps) ---

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

func login(issuer, clientID string) (*tokenResponse, error) {
	verifier, challenge, err := pkce()
	if err != nil {
		return nil, fmt.Errorf("generate pkce: %w", err)
	}

	redirectURI := fmt.Sprintf("http://localhost:%d/callback", callbackPort)
	authURL := buildAuthURL(issuer, clientID, redirectURI, challenge)

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	srv := &http.Server{Handler: mux}
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		if e := r.URL.Query().Get("error"); e != "" {
			fmt.Fprintf(w, "<html><body>Login failed: %s. You can close this tab.</body></html>", e)
			errCh <- fmt.Errorf("auth error: %s — %s", e, r.URL.Query().Get("error_description"))
			return
		}
		fmt.Fprint(w, "<html><body>Login successful! You can close this tab.</body></html>")
		codeCh <- r.URL.Query().Get("code")
	})

	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", callbackPort))
	if err != nil {
		return nil, fmt.Errorf("listen on port %d: %w", callbackPort, err)
	}
	go srv.Serve(ln) //nolint:errcheck
	defer srv.Shutdown(context.Background()) //nolint:errcheck

	fmt.Printf("Opening browser...\nIf it doesn't open, visit:\n  %s\n\n", authURL)
	openBrowser(authURL)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	select {
	case code := <-codeCh:
		endpoint := strings.TrimSuffix(issuer, "/") + "/protocol/openid-connect/token"
		form := url.Values{
			"grant_type":    {"authorization_code"},
			"code":          {code},
			"redirect_uri":  {redirectURI},
			"client_id":     {clientID},
			"code_verifier": {verifier},
		}
		return postForm(endpoint, form)
	case err := <-errCh:
		return nil, err
	case <-ctx.Done():
		return nil, fmt.Errorf("login timed out")
	}
}

func buildAuthURL(issuer, clientID, redirectURI, challenge string) string {
	base := strings.TrimSuffix(issuer, "/") + "/protocol/openid-connect/auth"
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"scope":                 {"openid email profile"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	return base + "?" + q.Encode()
}

func postForm(endpoint string, form url.Values) (*tokenResponse, error) {
	resp, err := http.PostForm(endpoint, form)
	if err != nil {
		return nil, fmt.Errorf("post %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if tr.Error != "" {
		return nil, fmt.Errorf("%s: %s", tr.Error, tr.ErrorDesc)
	}
	return &tr, nil
}

func pkce() (verifier, challenge string, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return
	}
	verifier = base64.RawURLEncoding.EncodeToString(buf)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return
}

func openBrowser(rawURL string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", rawURL)
	case "linux":
		cmd = exec.Command("xdg-open", rawURL)
	default:
		return
	}
	_ = cmd.Start()
}

func requireEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		fatalf("missing required env var: %s", key)
	}
	return v
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}
