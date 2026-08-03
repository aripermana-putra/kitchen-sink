// keycloak-session-revoke is a minimal PoC for MCUCP-238 B01.
//
// It runs two independent flows against OneCloud Keycloak QA to confirm that:
//   - Flow A: POST /logout revokes the refresh token (end-session endpoint)
//   - Flow B: POST /revoke revokes the refresh token (RFC 7009 endpoint)
//
// Each flow: login (PKCE) → call revocation endpoint → attempt refresh → verify 400 invalid_grant
//
// Usage:
//
//	KEYCLOAK_ISSUER=https://qa2-accounts-onecloud.rakuten-it.com/auth/realms/roc \
//	KEYCLOAK_CLIENT_ID=<client-id> \ qa is -> rns:roc:portal
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
	horizonBase := requireEnv("HORIZON_BASE_URL")

	flowUCPSubscriptionDiscovery(issuer, clientID, horizonBase)
}

// flowUCPSubscriptionDiscovery tests whether Horizon Core Data can serve as the source
// of truth for UCP service subscription status, removing the need for ucp_registered_tenants.
//
// Since UCP is not yet subscribed in any test tenant, DBaaS is used as a proxy:
// clsd-ucp is subscribed to DBaaS, but the test user has no DBaaS role.
// This mirrors the target scenario: tenant subscribed to UCP, user has no UCP role.
func flowUCPSubscriptionDiscovery(issuer, clientID, horizonBase string) {
	fmt.Println("\n========================================")
	fmt.Println("Flow: UCP subscription discovery via Core Data")
	fmt.Println("========================================")

	fmt.Println("\n--- Step 1: Login (PKCE) ---")
	tokens, err := login(issuer, clientID)
	if err != nil {
		fatalf("login failed: %v", err)
	}

	// Parse JWT groups and email
	jwtGroups, email := parseGroupsAndEmail(tokens.AccessToken)
	fmt.Printf("Logged in as: %s\n", email)
	fmt.Printf("JWT groups (%d entries):\n", len(jwtGroups))
	for _, g := range jwtGroups {
		fmt.Printf("  %s\n", g)
	}

	fmt.Println("\n--- Step 2: Check JWT for dbaas entries (proxy for UCP) ---")
	dbaasInJWT := false
	for _, g := range jwtGroups {
		if strings.Contains(g, ":dbaas:") {
			fmt.Printf("  FOUND in JWT: %s\n", g)
			dbaasInJWT = true
		}
	}
	if !dbaasInJWT {
		fmt.Println("  SC-2 PASS — dbaas absent from JWT groups (user has no DBaaS role) ✓")
	} else {
		fmt.Println("  SC-2 FAIL — dbaas present in JWT groups (user still has a DBaaS role)")
	}

	fmt.Println("\n--- Step 3: Check JWT for iam entry (tenant membership) ---")
	for _, g := range jwtGroups {
		if strings.Contains(g, ":iam:") {
			fmt.Printf("  SC-3 PASS — iam entry: %s ✓\n", g)
		}
	}

	fmt.Println("\n--- Step 4: Call Horizon Core Data for tenant subscriptions ---")
	// Use RNS format: rns:roc:iam:::users:{username} (derived from email prefix)
	username := strings.Split(email, "@")[0]
	memberRNS := "rns:roc:iam:::users:" + username
	endpoint := strings.TrimSuffix(horizonBase, "/") + "/v0/members/" + url.PathEscape(memberRNS) + "/tenants?subscriptions=true"
	fmt.Printf("  GET %s\n", endpoint)

	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		fatalf("build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+tokens.AccessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fatalf("horizon request failed: %v\n\nNote: ensure you are on Rakuten VPN / internal network.", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		fmt.Printf("  Horizon returned %d: %s\n", resp.StatusCode, strings.TrimSpace(string(body)))
		return
	}

	var horizonResp struct {
		TotalItems int `json:"total_items"`
		Items      []struct {
			Name          string `json:"name"`
			RNS           string `json:"rns"`
			Subscriptions []struct {
				Name string `json:"name"`
				RNS  string `json:"rns"`
			} `json:"subscriptions"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &horizonResp); err != nil {
		fatalf("parse horizon response: %v", err)
	}

	fmt.Printf("  Horizon returned %d tenant(s)\n", horizonResp.TotalItems)
	for _, tenant := range horizonResp.Items {
		fmt.Printf("\n  Tenant: %s (%s)\n", tenant.Name, tenant.RNS)
		fmt.Printf("  Subscriptions (%d):\n", len(tenant.Subscriptions))

		dbaasInSubscriptions := false
		ucpInSubscriptions := false
		for _, sub := range tenant.Subscriptions {
			fmt.Printf("    - %s\n", sub.Name)
			if sub.Name == "dbaas" {
				dbaasInSubscriptions = true
			}
			if sub.Name == "ucp" {
				ucpInSubscriptions = true
			}
		}

		fmt.Println()
		if dbaasInSubscriptions {
			fmt.Println("  SC-1 PASS — dbaas in subscriptions (tenant is subscribed) ✓")
		} else {
			fmt.Println("  SC-1 FAIL — dbaas not in subscriptions")
		}
		if ucpInSubscriptions {
			fmt.Println("  UCP — present in subscriptions ✓")
		} else {
			fmt.Println("  UCP — not yet subscribed in this tenant")
		}
	}

	fmt.Println("\n--- Verdict ---")
	if !dbaasInJWT {
		fmt.Println("Decoupling confirmed: subscription status (Horizon) is independent of user role (JWT groups).")
		fmt.Println("A service can be subscribed in a tenant while the user has no role in it.")
		fmt.Println("→ ucp tenants list must call Horizon to get subscription status.")
		fmt.Println("→ ucp_registered_tenants table is not needed.")
	}
}

// flowGroupsClaim logs in and prints the full groups claim from the access token.
// Used to verify what happens when a user has no role in a subscribed service (e.g. dbaas).
func flowGroupsClaim(issuer, clientID string) {
	fmt.Println("\n========================================")
	fmt.Println("Flow: inspect groups claim")
	fmt.Println("========================================")

	fmt.Println("\n--- Step 1: Login (PKCE) ---")
	tokens, err := login(issuer, clientID)
	if err != nil {
		fatalf("login failed: %v", err)
	}

	parts := strings.Split(tokens.AccessToken, ".")
	if len(parts) != 3 {
		fatalf("invalid JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		fatalf("base64 decode: %v", err)
	}

	var claims struct {
		Sub    string   `json:"sub"`
		Email  string   `json:"email"`
		Groups []string `json:"groups"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		fatalf("json parse: %v", err)
	}

	fmt.Printf("\nUser: %s (%s)\n", claims.Email, claims.Sub)
	fmt.Printf("\ngroups claim (%d entries):\n", len(claims.Groups))
	for _, g := range claims.Groups {
		fmt.Printf("  %s\n", g)
	}

	fmt.Println("\n--- Checking dbaas entries ---")
	found := false
	for _, g := range claims.Groups {
		if strings.Contains(g, ":dbaas:") {
			fmt.Printf("  FOUND: %s\n", g)
			found = true
		}
	}
	if !found {
		fmt.Println("  dbaas: (no entries — service is subscribed but user has no role assigned)")
	}
}

// flowAuthTimeSidStability verifies that auth_time and sid are stable across a token refresh.
func flowAuthTimeSidStability(issuer, clientID string) {
	fmt.Println("\n========================================")
	fmt.Println("Flow: auth_time + sid stability across refresh")
	fmt.Println("========================================")

	fmt.Println("\n--- Step 1: Login (PKCE) ---")
	tokens, err := login(issuer, clientID)
	if err != nil {
		fatalf("login failed: %v", err)
	}
	fmt.Println("Original access_token claims:")
	printAuthClaims(tokens.AccessToken)

	fmt.Println("\n--- Step 2: Refresh the access token ---")
	endpoint := strings.TrimSuffix(issuer, "/") + "/protocol/openid-connect/token"
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {tokens.RefreshToken},
		"client_id":     {clientID},
	}
	refreshed, err := postForm(endpoint, form)
	if err != nil {
		fatalf("refresh failed: %v", err)
	}
	fmt.Println("Refreshed access_token claims:")
	printAuthClaims(refreshed.AccessToken)

	fmt.Println("\n--- Verdict ---")
	orig := parseAuthClaims(tokens.AccessToken)
	ref := parseAuthClaims(refreshed.AccessToken)
	if orig.AuthTime != 0 && orig.AuthTime == ref.AuthTime {
		fmt.Println("auth_time: STABLE across refresh ✓")
	} else {
		fmt.Printf("auth_time: CHANGED — original=%d refreshed=%d\n", orig.AuthTime, ref.AuthTime)
	}
	if orig.Sid != "" && orig.Sid == ref.Sid {
		fmt.Println("sid:       STABLE across refresh ✓")
	} else {
		fmt.Printf("sid:       CHANGED — original=%q refreshed=%q\n", orig.Sid, ref.Sid)
	}
	if orig.Iat != ref.Iat {
		fmt.Println("iat:       CHANGED (expected — new token issued) ✓")
	} else {
		fmt.Println("iat:       UNCHANGED (unexpected)")
	}
}

type authClaims struct {
	Iat      int64  `json:"iat"`
	AuthTime int64  `json:"auth_time"`
	Sid      string `json:"sid"`
}

func parseGroupsAndEmail(tokenStr string) ([]string, string) {
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return nil, ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, ""
	}
	var c struct {
		Email  string   `json:"email"`
		Groups []string `json:"groups"`
	}
	_ = json.Unmarshal(payload, &c)
	return c.Groups, c.Email
}

func parseAuthClaims(tokenStr string) authClaims {
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return authClaims{}
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return authClaims{}
	}
	var c authClaims
	_ = json.Unmarshal(payload, &c)
	return c
}

func flowLogout(issuer, clientID string) {
	fmt.Println("\n========================================")
	fmt.Println("Flow A: /logout endpoint")
	fmt.Println("========================================")

	fmt.Println("\n--- Step 1: Login (PKCE) ---")
	tokens, err := login(issuer, clientID)
	if err != nil {
		fatalf("login failed: %v", err)
	}
	fmt.Printf("access_token:  %s...\n", tokens.AccessToken[:min(20, len(tokens.AccessToken))])
	fmt.Printf("refresh_token: %s...\n", tokens.RefreshToken[:min(20, len(tokens.RefreshToken))])
	printTokenTTL("access_token", tokens.AccessToken)
	printTokenTTL("refresh_token", tokens.RefreshToken)
	fmt.Println("access_token claims:")
	printAuthClaims(tokens.AccessToken)

	fmt.Println("\n--- Step 2: POST /logout with refresh_token ---")
	status, body, err := postRevoke(
		strings.TrimSuffix(issuer, "/")+"/protocol/openid-connect/logout",
		url.Values{"client_id": {clientID}, "refresh_token": {tokens.RefreshToken}},
	)
	if err != nil {
		fatalf("logout request failed: %v", err)
	}
	fmt.Printf("status: %d\nbody:   %s\n", status, strings.TrimSpace(body))

	fmt.Println("\n--- Step 3: Verify — attempt refresh with revoked token ---")
	printVerifyResult(tryRefresh(issuer, clientID, tokens.RefreshToken))
}

func flowRevoke(issuer, clientID string) {
	fmt.Println("\n========================================")
	fmt.Println("Flow B: /revoke endpoint (RFC 7009)")
	fmt.Println("========================================")

	fmt.Println("\n--- Step 1: Login (PKCE) ---")
	tokens, err := login(issuer, clientID)
	if err != nil {
		fatalf("login failed: %v", err)
	}
	fmt.Printf("access_token:  %s...\n", tokens.AccessToken[:min(20, len(tokens.AccessToken))])
	fmt.Printf("refresh_token: %s...\n", tokens.RefreshToken[:min(20, len(tokens.RefreshToken))])
	printTokenTTL("access_token", tokens.AccessToken)
	printTokenTTL("refresh_token", tokens.RefreshToken)
	fmt.Println("access_token claims:")
	printAuthClaims(tokens.AccessToken)

	fmt.Println("\n--- Step 2: POST /revoke with refresh_token ---")
	status, body, err := postRevoke(
		strings.TrimSuffix(issuer, "/")+"/protocol/openid-connect/revoke",
		url.Values{"client_id": {clientID}, "token": {tokens.RefreshToken}, "token_type_hint": {"refresh_token"}},
	)
	if err != nil {
		fatalf("revoke request failed: %v", err)
	}
	fmt.Printf("status: %d\nbody:   %s\n", status, strings.TrimSpace(body))

	fmt.Println("\n--- Step 3: Verify — attempt refresh with revoked token ---")
	printVerifyResult(tryRefresh(issuer, clientID, tokens.RefreshToken))
}

func flowOfflineLogout(issuer, clientID string) {
	fmt.Println("\n========================================")
	fmt.Println("Flow C: offline_access token + /logout")
	fmt.Println("========================================")

	fmt.Println("\n--- Step 1: Login (PKCE + offline_access scope) ---")
	tokens, err := loginWithScope(issuer, clientID, "openid email profile offline_access")
	if err != nil {
		fatalf("login failed: %v", err)
	}
	fmt.Printf("access_token:  %s...\n", tokens.AccessToken[:min(20, len(tokens.AccessToken))])
	fmt.Printf("refresh_token: %s...\n", tokens.RefreshToken[:min(20, len(tokens.RefreshToken))])
	printTokenTTL("access_token", tokens.AccessToken)
	printTokenTTL("refresh_token", tokens.RefreshToken)

	fmt.Println("\n--- Step 2: POST /logout with offline refresh_token ---")
	status, body, err := postRevoke(
		strings.TrimSuffix(issuer, "/")+"/protocol/openid-connect/logout",
		url.Values{"client_id": {clientID}, "refresh_token": {tokens.RefreshToken}},
	)
	if err != nil {
		fatalf("logout request failed: %v", err)
	}
	fmt.Printf("status: %d\nbody:   %s\n", status, strings.TrimSpace(body))

	fmt.Println("\n--- Step 3: Verify — attempt refresh with revoked offline token ---")
	printVerifyResult(tryRefresh(issuer, clientID, tokens.RefreshToken))
}

func printVerifyResult(status int, body string, err error) {
	if err != nil {
		fatalf("refresh request failed: %v", err)
	}
	fmt.Printf("status: %d\nbody:   %s\n", status, strings.TrimSpace(body))
	if status == http.StatusBadRequest && strings.Contains(body, "invalid_grant") {
		fmt.Println("RESULT: PASS — refresh token is revoked (400 invalid_grant)")
	} else {
		fmt.Printf("RESULT: FAIL — expected 400 invalid_grant, got %d\n", status)
	}
}

// --- PKCE login ---

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	ExpiresIn    int    `json:"expires_in"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

func login(issuer, clientID string) (*tokenResponse, error) {
	return loginWithScope(issuer, clientID, "openid email profile")
}

func loginWithScope(issuer, clientID, scope string) (*tokenResponse, error) {
	verifier, challenge, err := pkce()
	if err != nil {
		return nil, fmt.Errorf("generate pkce: %w", err)
	}

	redirectURI := fmt.Sprintf("http://localhost:%d/callback", callbackPort)
	authURL := buildAuthURLWithScope(issuer, clientID, redirectURI, challenge, scope)

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	srv := &http.Server{Addr: fmt.Sprintf(":%d", callbackPort), Handler: mux}
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
		return exchangeCode(issuer, clientID, code, verifier, redirectURI)
	case err := <-errCh:
		return nil, err
	case <-ctx.Done():
		return nil, fmt.Errorf("login timed out")
	}
}

func exchangeCode(issuer, clientID, code, verifier, redirectURI string) (*tokenResponse, error) {
	endpoint := strings.TrimSuffix(issuer, "/") + "/protocol/openid-connect/token"
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {clientID},
		"code_verifier": {verifier},
	}
	return postForm(endpoint, form)
}

// --- Revocation ---

// postRevoke sends a form POST to a revocation endpoint and returns the raw status and body.
func postRevoke(endpoint string, form url.Values) (int, string, error) {
	resp, err := http.PostForm(endpoint, form)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body), nil
}

// --- Verify revocation ---

func tryRefresh(issuer, clientID, refreshToken string) (int, string, error) {
	endpoint := strings.TrimSuffix(issuer, "/") + "/protocol/openid-connect/token"
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {clientID},
	}
	resp, err := http.PostForm(endpoint, form)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body), nil
}

// --- Token TTL ---

// printTokenTTL decodes the JWT payload and prints iat, exp, and TTL.
func printTokenTTL(label, tokenStr string) {
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		fmt.Printf("%s TTL: (could not parse JWT)\n", label)
		return
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		fmt.Printf("%s TTL: (base64 decode error: %v)\n", label, err)
		return
	}
	var claims struct {
		Iat int64 `json:"iat"`
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		fmt.Printf("%s TTL: (json parse error: %v)\n", label, err)
		return
	}
	ttl := time.Duration(claims.Exp-claims.Iat) * time.Second
	exp := time.Unix(claims.Exp, 0)
	fmt.Printf("%s TTL: %s (expires at %s)\n", label, ttl, exp.Format(time.RFC3339))
}

// printAuthClaims decodes the access token JWT and prints auth_time and session_state.
func printAuthClaims(tokenStr string) {
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		fmt.Println("auth claims: (could not parse JWT)")
		return
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		fmt.Printf("auth claims: (base64 decode error: %v)\n", err)
		return
	}
	var claims struct {
		Iat          int64  `json:"iat"`
		AuthTime     int64  `json:"auth_time"`
		SessionState string `json:"session_state"`
		Sid          string `json:"sid"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		fmt.Printf("auth claims: (json parse error: %v)\n", err)
		return
	}
	fmt.Printf("  iat:           %s\n", time.Unix(claims.Iat, 0).Format(time.RFC3339))
	if claims.AuthTime != 0 {
		fmt.Printf("  auth_time:     %s\n", time.Unix(claims.AuthTime, 0).Format(time.RFC3339))
	} else {
		fmt.Println("  auth_time:     (not present)")
	}
	if claims.SessionState != "" {
		fmt.Printf("  session_state: %s\n", claims.SessionState)
	} else {
		fmt.Println("  session_state: (not present)")
	}
	if claims.Sid != "" {
		fmt.Printf("  sid:           %s\n", claims.Sid)
	} else {
		fmt.Println("  sid:           (not present)")
	}
}

// --- Helpers ---

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
		return nil, fmt.Errorf("token error %s: %s", tr.Error, tr.ErrorDesc)
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

func buildAuthURLWithScope(issuer, clientID, redirectURI, challenge, scope string) string {
	base := strings.TrimSuffix(issuer, "/") + "/protocol/openid-connect/auth"
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"scope":                 {scope},
	}
	return base + "?" + q.Encode()
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

