// Package httpx holds small HTTP helpers shared by the API and web layers.
package httpx

import "net/http"

// PublicBase returns the public origin (scheme://host, no trailing slash) for
// building absolute URLs in emails, canonical tags, and sitemaps. A configured
// origin (PUBLIC_BASE_URL) is authoritative; otherwise it is derived from the
// request, honoring a TLS-terminating proxy's X-Forwarded-Proto/Host (Coolify's
// Traefik sets both). Configure the origin in production to be immune to
// Host-header spoofing.
func PublicBase(r *http.Request, configured string) string {
	if configured != "" {
		return configured
	}
	scheme := "http"
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	} else if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	if fwd := r.Header.Get("X-Forwarded-Host"); fwd != "" {
		host = fwd
	}
	return scheme + "://" + host
}
