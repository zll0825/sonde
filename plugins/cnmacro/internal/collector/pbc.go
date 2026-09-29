package collector

import (
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"

	"sonde/pkg/provider"
)

const (
	defaultPBCBase = "https://www.pbc.gov.cn"
	providerPBC    = "pbc"
)

// NewPBCHTTPClient builds the SafeHTTPClient shared by both PBoC collectors
// (one token bucket for the host). pbc.gov.cn answers some first requests
// with a cookie-setting redirect (and http→https 302), so the underlying
// client carries a cookie jar and follows redirects (net/http default).
func NewPBCHTTPClient() *provider.SafeHTTPClient {
	cfg := provider.PBOCConfig()
	return provider.NewSafeHTTPClientWithHTTPClient(cfg, withCookieJar(&http.Client{Timeout: cfg.Timeout}))
}

func withCookieJar(hc *http.Client) *http.Client {
	if hc.Jar == nil {
		jar, err := cookiejar.New(nil)
		if err == nil {
			hc.Jar = jar
		}
	}
	return hc
}

func pbcGetHTML(ctx context.Context, client *provider.SafeHTTPClient, rawURL string) (string, error) {
	f, err := doGET(ctx, client, rawURL, "text/html,application/xhtml+xml,*/*;q=0.8")
	if err != nil {
		return "", err
	}
	return decodeHTML(f)
}

// resolveURL resolves a site-relative href against base.
func resolveURL(base, href string) (string, error) {
	b, err := url.Parse(strings.TrimRight(base, "/") + "/")
	if err != nil {
		return "", err
	}
	h, err := url.Parse(strings.TrimSpace(href))
	if err != nil {
		return "", fmt.Errorf("bad href %q: %w", href, err)
	}
	return b.ResolveReference(h).String(), nil
}

var anchorRe = regexp.MustCompile(`(?is)<a\s[^>]*?href\s*=\s*['"]([^'"]+)['"][^>]*>(.*?)</a>`)

type anchor struct {
	href string
	text string
}

func anchors(page string) []anchor {
	var out []anchor
	for _, m := range anchorRe.FindAllStringSubmatch(page, -1) {
		out = append(out, anchor{href: m[1], text: htmlText(m[2])})
	}
	return out
}
