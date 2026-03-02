// csp.go constructs Content-Security-Policy headers for sandboxed UI views.
//
// The default policy is restrictive (connect-src 'none', frame-src 'none',
// object-src 'none'). Servers opt in to additional capabilities by declaring
// domains in the resource's _meta.ui.csp field; only declared domains are
// added to the policy.
package host

import "strings"

// ResourceCSP holds the CSP metadata from a UI resource's _meta.ui.csp field.
type ResourceCSP struct {
	ResourceDomains []string `json:"resourceDomains,omitempty"`
	ConnectDomains  []string `json:"connectDomains,omitempty"`
	FrameDomains    []string `json:"frameDomains,omitempty"`
	BaseURIDomains  []string `json:"baseUriDomains,omitempty"`
}

// buildCSP constructs a Content-Security-Policy header value from the resource's
// CSP metadata.
//
// The nil vs non-nil distinction is meaningful per the spec:
//   - nil (ui.csp omitted entirely): the server made no CSP declaration, so we
//     use a fully restrictive default (connect-src 'none').
//   - non-nil but empty (ui.csp present with no domains): the server explicitly
//     opted into CSP, so it gets 'self' as a baseline (connect-src 'self').
func buildCSP(csp *ResourceCSP) string {
	if csp == nil {
		return strings.Join([]string{
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
		}, "; ")
	}

	rd := strings.Join(csp.ResourceDomains, " ")
	cd := strings.Join(csp.ConnectDomains, " ")
	with := func(base, extra string) string {
		if extra != "" {
			return base + " " + extra
		}
		return base
	}

	frameSrc := "'none'"
	if len(csp.FrameDomains) > 0 {
		frameSrc = strings.Join(csp.FrameDomains, " ")
	}

	baseURI := "'self'"
	if len(csp.BaseURIDomains) > 0 {
		baseURI = strings.Join(csp.BaseURIDomains, " ")
	}

	return strings.Join([]string{
		"default-src 'none'",
		"script-src " + with("'self' 'unsafe-inline'", rd),
		"style-src " + with("'self' 'unsafe-inline'", rd),
		"connect-src " + with("'self'", cd),
		"img-src " + with("'self' data:", rd),
		"font-src " + with("'self'", rd),
		"media-src " + with("'self' data:", rd),
		"frame-src " + frameSrc,
		"object-src 'none'",
		"base-uri " + baseURI,
	}, "; ")
}
