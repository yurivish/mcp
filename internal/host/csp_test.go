// csp_test.go tests Content-Security-Policy header construction.
package host

import (
	"strings"
	"testing"
)

// nil CSP produces restrictive default policy (connect-src 'none', frame-src 'none', etc).
func TestBuildCSPDefault(t *testing.T) {
	csp := buildCSP(nil)

	checks := []string{
		"default-src 'none'",
		"script-src 'self' 'unsafe-inline'",
		"style-src 'self' 'unsafe-inline'",
		"connect-src 'none'",
		"img-src 'self' data:",
		"font-src 'self'",
		"media-src 'self' data:",
		"frame-src 'none'",
		"object-src 'none'",
		"base-uri 'self'",
	}
	for _, check := range checks {
		if !strings.Contains(csp, check) {
			t.Errorf("default CSP missing %q:\n  got: %s", check, csp)
		}
	}
}

func TestBuildCSPEmpty(t *testing.T) {
	// Empty ResourceCSP (no domains) should have self-only connect
	csp := buildCSP(&ResourceCSP{})

	if !strings.Contains(csp, "connect-src 'self'") {
		t.Errorf("empty CSP should have connect-src 'self', got: %s", csp)
	}
	if !strings.Contains(csp, "frame-src 'none'") {
		t.Errorf("empty CSP should have frame-src 'none', got: %s", csp)
	}
}

func TestBuildCSPWithDomains(t *testing.T) {
	csp := buildCSP(&ResourceCSP{
		ResourceDomains: []string{"https://cdn.example.com", "https://static.example.com"},
		ConnectDomains:  []string{"https://api.example.com"},
		FrameDomains:    []string{"https://embed.example.com"},
		BaseURIDomains:  []string{"https://base.example.com"},
	})

	checks := []struct {
		directive string
		domain    string
	}{
		{"script-src", "https://cdn.example.com"},
		{"script-src", "https://static.example.com"},
		{"style-src", "https://cdn.example.com"},
		{"connect-src", "https://api.example.com"},
		{"img-src", "https://cdn.example.com"},
		{"font-src", "https://cdn.example.com"},
		{"media-src", "https://cdn.example.com"},
		{"frame-src", "https://embed.example.com"},
		{"base-uri", "https://base.example.com"},
	}
	for _, check := range checks {
		if !strings.Contains(csp, check.domain) {
			t.Errorf("CSP %s missing %s:\n  got: %s", check.directive, check.domain, csp)
		}
	}

	// frame-src should NOT contain 'none' when domains are specified
	if strings.Contains(csp, "frame-src 'none'") {
		t.Errorf("frame-src should not be 'none' when domains specified: %s", csp)
	}
}

func TestBuildCSPNoDomainLeakage(t *testing.T) {
	// Verify that undeclared domains are NOT in the CSP
	csp := buildCSP(&ResourceCSP{
		ConnectDomains: []string{"https://allowed.com"},
	})

	// connect-src should have allowed.com
	if !strings.Contains(csp, "https://allowed.com") {
		t.Errorf("CSP missing declared domain: %s", csp)
	}

	// script-src should NOT have any external domains (no resource domains declared)
	scriptParts := extractDirective(csp, "script-src")
	if strings.Contains(scriptParts, "https://") {
		t.Errorf("script-src should not contain external domains when resourceDomains is empty: %s", scriptParts)
	}
}

// extractDirective extracts the value of a CSP directive.
func extractDirective(csp, directive string) string {
	for part := range strings.SplitSeq(csp, "; ") {
		if after, ok := strings.CutPrefix(part, directive+" "); ok {
			return after
		}
	}
	return ""
}
