package plugin

import "testing"

func TestOptionalHTTPSPolicyForPluginURLs(t *testing.T) {
	t.Setenv("TOKENHUB_REQUIRE_HTTPS", "true")
	if err := validatePluginDistributionURL("download_url", "http://plugins.example/plugin.zip"); err == nil {
		t.Fatal("HTTP distribution URL passed with HTTPS required")
	}
	t.Setenv("TOKENHUB_REQUIRE_HTTPS", "false")
	if err := validatePluginDistributionURL("download_url", "http://plugins.example/plugin.zip"); err != nil {
		t.Fatalf("HTTP distribution URL: %v", err)
	}
	if err := validatePluginMarketplaceURL("homepage_url", "http://plugins.example/home"); err != nil {
		t.Fatalf("HTTP marketplace URL: %v", err)
	}
	if err := validateMarketplaceImmutableHTTPSURL("artifact url", "http://plugins.example/releases/v1.zip"); err != nil {
		t.Fatalf("HTTP immutable URL: %v", err)
	}
	if err := validateMarketplaceImmutableHTTPSURL("artifact url", "http://plugins.example/latest.zip"); err == nil {
		t.Fatal("HTTP toggle bypassed immutable URL restrictions")
	}
}
