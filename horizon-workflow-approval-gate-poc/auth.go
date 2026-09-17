package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"golang.org/x/term"
)

// tokenResponse mirrors the relevant fields of a Keycloak token endpoint response.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
}

// login performs the OneCloud Keycloak Resource Owner Password Credentials
// flow, matching the curl example in the Workflow 2.39.0 Tutorial
// (https://confluence.rakuten-it.com/confluence/pages/viewpage.action?pageId=6881495341).
//
// The resulting token is used directly against the Workflow API. Whether it
// carries the tenant-admin / team-admin roles needed to act as an approver
// depends on the OneCloud account used to log in — this PoC assumes the
// operator is both, per the design doc.
func login(issuer, clientID string) (string, error) {
	username, err := prompt("OneCloud username: ")
	if err != nil {
		return "", fmt.Errorf("read username: %w", err)
	}
	password, err := promptSecret("OneCloud password: ")
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}

	form := url.Values{}
	form.Set("grant_type", "password")
	form.Set("client_id", clientID)
	form.Set("username", username)
	form.Set("password", password)

	tokenURL := strings.TrimRight(issuer, "/") + "/protocol/openid-connect/token"
	resp, err := http.PostForm(tokenURL, form)
	if err != nil {
		return "", fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token request failed (%d): %s", resp.StatusCode, string(body))
	}

	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if tr.AccessToken == "" {
		return "", fmt.Errorf("token response had no access_token: %s", string(body))
	}
	return tr.AccessToken, nil
}

// emailFromToken reads the "email" claim out of a JWT's payload, without
// verifying the signature — this PoC already trusts the token because it
// just received it from the issuer over TLS; the decode here is purely to
// print/use the caller's own email for the /jobs visibility check.
func emailFromToken(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("token does not look like a JWT (want 3 dot-separated parts, got %d)", len(parts))
	}
	payload := parts[1]
	if m := len(payload) % 4; m != 0 {
		payload += strings.Repeat("=", 4-m)
	}
	decoded, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		return "", fmt.Errorf("decode JWT payload: %w", err)
	}
	var claims struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return "", fmt.Errorf("unmarshal JWT claims: %w", err)
	}
	if claims.Email == "" {
		return "", fmt.Errorf("JWT had no email claim")
	}
	return claims.Email, nil
}

func prompt(label string) (string, error) {
	fmt.Print(label)
	var s string
	_, err := fmt.Scanln(&s)
	return s, err
}

func promptSecret(label string) (string, error) {
	fmt.Print(label)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	return string(b), err
}
