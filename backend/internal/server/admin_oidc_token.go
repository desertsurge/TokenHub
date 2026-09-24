package server

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"
)

type oidcJSONWebKey struct {
	Kty string `json:"kty"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
	K   string `json:"k"`
}

type oidcJWKS struct {
	Keys []oidcJSONWebKey `json:"keys"`
}

func verifyManagedOIDCToken(ctx context.Context, provider AdminResource, rawToken string, expectedNonce string) (map[string]any, error) {
	if strings.TrimSpace(rawToken) == "" {
		return nil, NewHTTPError(http.StatusForbidden, "oidc_id_token_missing", "Managed OIDC provider did not return an ID token")
	}
	if len(rawToken) > 64*1024 {
		return nil, NewHTTPError(http.StatusForbidden, "oidc_id_token_invalid", "Managed OIDC ID token is too large")
	}
	parts := strings.Split(rawToken, ".")
	if len(parts) != 3 {
		return nil, NewHTTPError(http.StatusForbidden, "oidc_id_token_invalid", "Managed OIDC ID token is malformed")
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, NewHTTPError(http.StatusForbidden, "oidc_id_token_invalid", "Managed OIDC ID token header is malformed")
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, NewHTTPError(http.StatusForbidden, "oidc_id_token_invalid", "Managed OIDC ID token claims are malformed")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, NewHTTPError(http.StatusForbidden, "oidc_id_token_invalid", "Managed OIDC ID token signature is malformed")
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
		Typ string `json:"typ"`
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil || strings.TrimSpace(header.Alg) == "" || strings.EqualFold(header.Alg, "none") {
		return nil, NewHTTPError(http.StatusForbidden, "oidc_id_token_invalid", "Managed OIDC ID token algorithm is invalid")
	}
	if header.Typ != "" && !strings.EqualFold(header.Typ, "JWT") {
		return nil, NewHTTPError(http.StatusForbidden, "oidc_id_token_invalid", "Managed OIDC ID token type is invalid")
	}
	var claims map[string]any
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return nil, NewHTTPError(http.StatusForbidden, "oidc_id_token_invalid", "Managed OIDC ID token claims are invalid")
	}
	jwksURL, err := managedOIDCJWKSURL(ctx, provider)
	if err != nil {
		return nil, err
	}
	keys, err := fetchOIDCJWKS(ctx, jwksURL)
	if err != nil {
		return nil, NewHTTPError(http.StatusBadGateway, "oidc_jwks_unavailable", "Managed OIDC signing keys are unavailable")
	}
	key, err := selectOIDCKey(keys, header.Kid, header.Alg)
	if err != nil {
		return nil, NewHTTPError(http.StatusForbidden, "oidc_id_token_invalid", err.Error())
	}
	if err := verifyOIDCSignature(header.Alg, key, []byte(parts[0]+"."+parts[1]), signature); err != nil {
		return nil, NewHTTPError(http.StatusForbidden, "oidc_id_token_invalid", "Managed OIDC ID token signature is invalid")
	}
	if err := validateOIDCClaims(claims, strings.TrimSpace(stringField(provider.Fields, "issuer_url")), strings.TrimSpace(stringField(provider.Fields, "client_id")), expectedNonce, time.Now().UTC()); err != nil {
		return nil, err
	}
	return claims, nil
}

func managedOIDCJWKSURL(ctx context.Context, provider AdminResource) (string, error) {
	configured := strings.TrimSpace(stringField(provider.Fields, "jwks_url"))
	if configured == "" {
		configured = strings.TrimSpace(stringField(provider.Fields, "jwks_uri"))
	}
	if configured != "" {
		if !validGatewayManagedOIDCURL(configured) {
			return "", NewHTTPError(http.StatusBadRequest, "gateway_identity_provider_invalid", "Managed OIDC JWKS URL is not trusted")
		}
		return configured, nil
	}
	discoveryURL := strings.TrimSpace(stringField(provider.Fields, "discovery_url"))
	if discoveryURL == "" {
		return "", NewHTTPError(http.StatusBadRequest, "gateway_identity_provider_invalid", "Managed OIDC provider requires a JWKS or discovery URL")
	}
	if !validGatewayManagedOIDCURL(discoveryURL) {
		return "", NewHTTPError(http.StatusBadRequest, "gateway_identity_provider_invalid", "Managed OIDC discovery URL is not trusted")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		return "", err
	}
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("OIDC discovery returned HTTP %d", response.StatusCode)
	}
	var document struct {
		Issuer  string `json:"issuer"`
		JWKSURI string `json:"jwks_uri"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&document); err != nil {
		return "", err
	}
	if normalizeOIDCIssuer(document.Issuer) != normalizeOIDCIssuer(stringField(provider.Fields, "issuer_url")) || !validGatewayManagedOIDCURL(document.JWKSURI) {
		return "", NewHTTPError(http.StatusBadRequest, "gateway_identity_provider_invalid", "OIDC discovery metadata does not match the configured issuer")
	}
	return document.JWKSURI, nil
}

func fetchOIDCJWKS(ctx context.Context, jwksURL string) ([]oidcJSONWebKey, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("accept", "application/json")
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("OIDC JWKS returned HTTP %d", response.StatusCode)
	}
	var document oidcJWKS
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&document); err != nil {
		return nil, err
	}
	if len(document.Keys) == 0 {
		return nil, fmt.Errorf("OIDC JWKS contains no keys")
	}
	return document.Keys, nil
}

func selectOIDCKey(keys []oidcJSONWebKey, kid string, alg string) (oidcJSONWebKey, error) {
	var selected *oidcJSONWebKey
	for index := range keys {
		key := keys[index]
		if key.Use != "" && key.Use != "sig" {
			continue
		}
		if key.Alg != "" && key.Alg != alg {
			continue
		}
		if kid != "" && key.Kid != kid {
			continue
		}
		if selected != nil {
			return oidcJSONWebKey{}, fmt.Errorf("OIDC signing key is ambiguous")
		}
		selected = &keys[index]
	}
	if selected == nil {
		return oidcJSONWebKey{}, fmt.Errorf("OIDC signing key was not found")
	}
	return *selected, nil
}

func verifyOIDCSignature(alg string, key oidcJSONWebKey, signingInput, signature []byte) error {
	switch alg {
	case "RS256", "RS384", "RS512":
		if key.Kty != "RSA" {
			return fmt.Errorf("OIDC signing key type does not match algorithm")
		}
		n, err := decodeOIDCInt(key.N)
		if err != nil {
			return err
		}
		e, err := decodeOIDCInt(key.E)
		if err != nil || !e.IsInt64() || e.Int64() < 3 || e.Int64() > 1<<31-1 {
			return fmt.Errorf("OIDC RSA exponent is invalid")
		}
		if n.BitLen() < 2048 {
			return fmt.Errorf("OIDC RSA signing key is too small")
		}
		hash, hashID, err := oidcHash(alg, signingInput)
		if err != nil {
			return err
		}
		return rsa.VerifyPKCS1v15(&rsa.PublicKey{N: n, E: int(e.Int64())}, hashID, hash, signature)
	case "ES256", "ES384", "ES512":
		if key.Kty != "EC" {
			return fmt.Errorf("OIDC signing key type does not match algorithm")
		}
		curveConfig, ok := map[string]struct {
			curve elliptic.Curve
			size  int
		}{"ES256": {elliptic.P256(), 32}, "ES384": {elliptic.P384(), 48}, "ES512": {elliptic.P521(), 66}}[alg]
		if !ok {
			return fmt.Errorf("unsupported OIDC EC algorithm")
		}
		curve, size := curveConfig.curve, curveConfig.size
		x, err := decodeOIDCInt(key.X)
		if err != nil {
			return err
		}
		y, err := decodeOIDCInt(key.Y)
		if err != nil || !curve.IsOnCurve(x, y) || len(signature) != size*2 {
			return fmt.Errorf("OIDC EC signing key is invalid")
		}
		hash, _, err := oidcHash(alg, signingInput)
		if err != nil {
			return err
		}
		return verifyECDSA(&ecdsa.PublicKey{Curve: curve, X: x, Y: y}, hash, signature[:size], signature[size:])
	case "EdDSA":
		if key.Kty != "OKP" || key.Crv != "Ed25519" {
			return fmt.Errorf("OIDC signing key type does not match algorithm")
		}
		publicKey, err := base64.RawURLEncoding.DecodeString(key.X)
		if err != nil || len(publicKey) != ed25519.PublicKeySize || !ed25519.Verify(ed25519.PublicKey(publicKey), signingInput, signature) {
			return fmt.Errorf("OIDC EdDSA signature is invalid")
		}
		return nil
	default:
		return fmt.Errorf("OIDC signing algorithm %q is not allowed", alg)
	}
}

func decodeOIDCInt(value string) (*big.Int, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) == 0 {
		return nil, fmt.Errorf("OIDC JWK integer is invalid")
	}
	return new(big.Int).SetBytes(decoded), nil
}

func oidcHash(alg string, input []byte) ([]byte, crypto.Hash, error) {
	var hash crypto.Hash
	switch alg {
	case "RS256", "ES256":
		hash = crypto.SHA256
	case "RS384", "ES384":
		hash = crypto.SHA384
	case "RS512", "ES512":
		hash = crypto.SHA512
	default:
		return nil, 0, fmt.Errorf("unsupported OIDC hash algorithm")
	}
	if !hash.Available() {
		return nil, 0, fmt.Errorf("OIDC hash is unavailable")
	}
	digest := hash.New()
	_, _ = digest.Write(input)
	return digest.Sum(nil), hash, nil
}

func verifyECDSA(key *ecdsa.PublicKey, digest, rBytes, sBytes []byte) error {
	if ecdsa.Verify(key, digest, new(big.Int).SetBytes(rBytes), new(big.Int).SetBytes(sBytes)) {
		return nil
	}
	return fmt.Errorf("OIDC ECDSA signature is invalid")
}

func validateOIDCClaims(claims map[string]any, issuer string, clientID string, expectedNonce string, now time.Time) error {
	if normalizeOIDCIssuer(firstOAuthClaim(claims, "iss")) != normalizeOIDCIssuer(issuer) || strings.TrimSpace(issuer) == "" {
		return NewHTTPError(http.StatusForbidden, "oidc_issuer_mismatch", "Managed OIDC issuer does not match the configured issuer")
	}
	if strings.TrimSpace(firstOAuthClaim(claims, "sub")) == "" {
		return NewHTTPError(http.StatusForbidden, "oidc_subject_missing", "Managed OIDC ID token does not contain a subject")
	}
	if !oidcAudienceContains(claims["aud"], clientID) {
		return NewHTTPError(http.StatusForbidden, "oidc_audience_mismatch", "Managed OIDC audience does not match the configured client")
	}
	if strings.TrimSpace(expectedNonce) == "" || firstOAuthClaim(claims, "nonce") != strings.TrimSpace(expectedNonce) {
		return NewHTTPError(http.StatusForbidden, "oidc_nonce_mismatch", "Managed OIDC nonce does not match the login request")
	}
	expiresAt, ok := oidcNumericDate(claims["exp"])
	if !ok || now.After(time.Unix(expiresAt, 0).Add(time.Minute)) {
		return NewHTTPError(http.StatusForbidden, "oidc_token_expired", "Managed OIDC ID token is expired")
	}
	if notBefore, present := oidcNumericDate(claims["nbf"]); present && now.Before(time.Unix(notBefore, 0).Add(-time.Minute)) {
		return NewHTTPError(http.StatusForbidden, "oidc_token_not_active", "Managed OIDC ID token is not active")
	}
	return nil
}

func oidcAudienceContains(value any, clientID string) bool {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return false
	}
	switch typed := value.(type) {
	case string:
		return typed == clientID
	case []any:
		for _, item := range typed {
			if item, ok := item.(string); ok && item == clientID {
				return true
			}
		}
	}
	return false
}

func oidcNumericDate(value any) (int64, bool) {
	switch typed := value.(type) {
	case float64:
		return int64(typed), typed > 0
	case json.Number:
		parsed, err := typed.Int64()
		return parsed, err == nil && parsed > 0
	case int64:
		return typed, typed > 0
	default:
		return 0, false
	}
}

func normalizeOIDCIssuer(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}
