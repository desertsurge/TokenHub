package securitypolicy

import (
	"os"
	"strings"
)

// RequireHTTPS enables HTTPS enforcement only when explicitly requested.
func RequireHTTPS() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("TOKENHUB_REQUIRE_HTTPS"))) {
	case "true", "1", "yes", "on":
		return true
	default:
		return false
	}
}
