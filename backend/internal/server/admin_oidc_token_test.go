package server

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestVerifyManagedOIDCTokenValidatesSignatureAndClaims(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	provider := managedGatewayProvider()
	provider.Fields["client_id"] = "client-1"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/jwks" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "use": "sig", "alg": "RS256", "kid": "key-1",
			"n": base64.RawURLEncoding.EncodeToString(privateKey.PublicKey.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString([]byte{1, 0, 1}),
		}}})
	}))
	defer server.Close()
	provider.Fields["jwks_url"] = server.URL + "/jwks"
	nonce := "nonce-1"
	token := signTestOIDCToken(t, privateKey, map[string]any{
		"iss": provider.Fields["issuer_url"], "sub": "subject-1", "aud": "client-1", "nonce": nonce,
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	claims, err := verifyManagedOIDCToken(t.Context(), provider, token, nonce)
	if err != nil || firstOAuthClaim(claims, "sub") != "subject-1" {
		t.Fatalf("valid managed ID token rejected: claims=%v err=%v", claims, err)
	}
}

func TestVerifyManagedOIDCTokenRejectsIssuerAudienceNonceAndSignature(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	provider := managedGatewayProvider()
	provider.Fields["client_id"] = "client-1"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "alg": "RS256", "kid": "key-1",
			"n": base64.RawURLEncoding.EncodeToString(privateKey.PublicKey.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString([]byte{1, 0, 1}),
		}}})
	}))
	defer server.Close()
	provider.Fields["jwks_url"] = server.URL
	base := map[string]any{"iss": provider.Fields["issuer_url"], "sub": "subject-1", "aud": "client-1", "nonce": "nonce-1", "exp": time.Now().Add(5 * time.Minute).Unix()}
	for name, mutate := range map[string]func(map[string]any){
		"issuer":   func(claims map[string]any) { claims["iss"] = "https://other.example.test" },
		"audience": func(claims map[string]any) { claims["aud"] = "other-client" },
		"nonce":    func(claims map[string]any) { claims["nonce"] = "other-nonce" },
	} {
		claims := cloneClaims(base)
		mutate(claims)
		if _, err := verifyManagedOIDCToken(t.Context(), provider, signTestOIDCToken(t, privateKey, claims), "nonce-1"); AsHTTPError(err).Status != http.StatusForbidden {
			t.Fatalf("%s mismatch was accepted: %v", name, err)
		}
	}
	valid := signTestOIDCToken(t, privateKey, base)
	parts := strings.Split(valid, ".")
	parts[2] = strings.Repeat("a", len(parts[2]))
	if _, err := verifyManagedOIDCToken(t.Context(), provider, strings.Join(parts, "."), "nonce-1"); AsHTTPError(err).Code != "oidc_id_token_invalid" {
		t.Fatalf("invalid signature was accepted: %v", err)
	}
}

func signTestOIDCToken(t *testing.T, privateKey *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","kid":"key-1","typ":"JWT"}`))
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	payloadEncoded := base64.RawURLEncoding.EncodeToString(payload)
	signingInput := header + "." + payloadEncoded
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func cloneClaims(source map[string]any) map[string]any {
	clone := make(map[string]any, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}
