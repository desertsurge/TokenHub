package securitypolicy

import "testing"

func TestRequireHTTPSDefaultsToOptional(t *testing.T) {
	for _, test := range []struct {
		value string
		want  bool
	}{
		{"", false},
		{"true", true},
		{"1", true},
		{"YES", true},
		{"ON", true},
		{"invalid", false},
		{"false", false},
		{"0", false},
		{"OFF", false},
	} {
		t.Setenv("TOKENHUB_REQUIRE_HTTPS", test.value)
		if got := RequireHTTPS(); got != test.want {
			t.Errorf("TOKENHUB_REQUIRE_HTTPS=%q: got %t, want %t", test.value, got, test.want)
		}
	}
}
