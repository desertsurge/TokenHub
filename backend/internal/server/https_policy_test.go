package server

import (
	"context"
	"net"
	"net/http"
	"testing"
)

func TestOptionalHTTPSPolicyForProviderUpstreams(t *testing.T) {
	t.Setenv("TOKENHUB_PROVIDER_UPSTREAM_ACCESS_MODE", "strict")
	t.Setenv("TOKENHUB_PROVIDER_UPSTREAM_ALLOWED_CIDRS", "")
	t.Setenv("TOKENHUB_REQUIRE_HTTPS", "")
	if err := ValidateProviderUpstreamBaseURL("http://192.169.1.1/v1"); err != nil {
		t.Fatalf("default public HTTP literal: %v", err)
	}
	t.Setenv("TOKENHUB_REQUIRE_HTTPS", "true")
	if code := AsHTTPError(ValidateProviderUpstreamBaseURL("http://192.169.1.1/v1")).Code; code != "provider_base_url_insecure_scheme" {
		t.Fatalf("HTTPS-required public literal error = %q", code)
	}
	if err := ValidateProviderUpstreamBaseURL("http://192.168.1.1/v1"); err != nil {
		t.Fatalf("HTTPS-required private literal exception: %v", err)
	}
	t.Setenv("TOKENHUB_REQUIRE_HTTPS", "false")
	for _, raw := range []string{"http://192.169.1.1/v1", "http://public.example/v1"} {
		if err := ValidateProviderUpstreamBaseURL(raw); err != nil {
			t.Fatalf("HTTP URL %q: %v", raw, err)
		}
	}
	if err := ValidateProviderUpstreamBaseURL("http://169.254.169.254/latest/meta-data"); err == nil {
		t.Fatal("HTTP toggle allowed metadata address")
	}
	if err := ValidateProviderUpstreamBaseURL("http://10.0.0.1/v1"); err != nil {
		t.Fatalf("HTTP toggle rejected permitted private literal: %v", err)
	}
	if err := ValidateProviderUpstreamBaseURL("ftp://192.169.1.1/v1"); err == nil {
		t.Fatal("HTTP toggle allowed an unsupported scheme")
	}
}

func TestAdminCreateProviderAcceptsPublicHTTPByDefault(t *testing.T) {
	t.Setenv("TOKENHUB_REQUIRE_HTTPS", "")
	t.Setenv("TOKENHUB_PROVIDER_UPSTREAM_ACCESS_MODE", "strict")
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	response := doJSON(t, New(store).Handler(), http.MethodPost, "/api/admin/providers", map[string]any{
		"name":     "Public HTTP provider",
		"type":     ProviderOpenAICompatible,
		"base_url": "http://192.169.1.1/v1",
	}, "")
	if response.Code != http.StatusCreated {
		t.Fatalf("default public HTTP provider create: status=%d body=%s", response.Code, response.Body)
	}
}

func TestOptionalHTTPSPolicyKeepsProviderDNSGuard(t *testing.T) {
	t.Setenv("TOKENHUB_REQUIRE_HTTPS", "false")
	t.Setenv("TOKENHUB_PROVIDER_UPSTREAM_ACCESS_MODE", "strict")
	called := false
	transport := guardProviderUpstreamRequests(roundTripperFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody}, nil
	}), allowedProviderUpstreamCIDRs()).(*providerUpstreamPolicyTransport)
	transport.lookup = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("192.169.1.1")}}, nil
	}
	request, _ := http.NewRequest(http.MethodGet, "http://public.example/v1", nil)
	if _, err := transport.RoundTrip(request); err != nil || !called {
		t.Fatalf("public HTTP request: called=%t err=%v", called, err)
	}
	called = false
	transport.lookup = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("169.254.169.254")}}, nil
	}
	if _, err := transport.RoundTrip(request); err == nil || called {
		t.Fatalf("metadata DNS target: called=%t err=%v", called, err)
	}
	transport.lookup = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("192.169.1.1")}, {IP: net.ParseIP("169.254.169.254")}}, nil
	}
	if _, err := transport.RoundTrip(request); err == nil || called {
		t.Fatalf("mixed public and metadata DNS targets: called=%t err=%v", called, err)
	}
}

func TestOptionalHTTPSPolicyAppliesToOtherURLValidators(t *testing.T) {
	t.Setenv("TOKENHUB_REQUIRE_HTTPS", "false")
	if _, err := validateOAuthCallbackURL("http://login.example/callback"); err != nil {
		t.Fatalf("OAuth callback: %v", err)
	}
	if !validGatewayManagedOIDCURL("http://login.example/issuer") {
		t.Fatal("managed OIDC URL was rejected")
	}
	if err := validatePluginInstallHTTPSURL("Plugin package", "http://plugins.example/plugin.zip"); err != nil {
		t.Fatalf("plugin URL: %v", err)
	}
	if got := sanitizeAdminPluginPublicHTTPSURL("http://plugins.example/home"); got != "http://plugins.example/home" {
		t.Fatalf("public plugin URL = %q", got)
	}
	if _, err := codexResponsesEndpoint(Provider{BaseURL: "http://chatgpt.com/backend-api/codex"}); err != nil {
		t.Fatalf("Codex URL: %v", err)
	}
	provider := Provider{BaseURL: "http://chatgpt.com/backend-api/codex"}
	if _, err := codexCompactEndpoint(provider); err != nil {
		t.Fatalf("Codex compact URL: %v", err)
	}
	if _, err := codexImageEndpoint(provider, false); err != nil {
		t.Fatalf("Codex image URL: %v", err)
	}
	if err := validateHTTPSURL("TOKENHUB_PLUGIN_MARKETPLACE_URL", "http://plugins.example/index.json"); err != nil {
		t.Fatalf("plugin marketplace configuration: %v", err)
	}
	if _, err := normalizeOpenAPIServerURL("http://public.example", false); err != nil {
		t.Fatalf("OpenAPI server URL: %v", err)
	}
	if err := validateNativeAssetURL("http://github.com/owner/release"); err != nil {
		t.Fatalf("release URL: %v", err)
	}
	if err := validateNativeAssetURL("http://untrusted.example/release"); err == nil {
		t.Fatal("HTTP toggle bypassed release host allowlist")
	}
}
