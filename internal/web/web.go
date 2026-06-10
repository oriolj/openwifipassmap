// Package web serves the small public, server-rendered site: a landing page
// that lists nearby spots (via browser geolocation) and a shareable per-spot
// page. This is the "public web meant to share" surface; the richer experience
// lives in the Capacitor mobile app.
package web

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/oriolj/openwifipassmap/internal/httpx"
	"github.com/oriolj/openwifipassmap/internal/models"
	"github.com/oriolj/openwifipassmap/internal/store"
)

//go:embed templates/*.html
var tmplFS embed.FS

// Web renders the public site.
type Web struct {
	store     *store.Store
	tmpl      *template.Template
	staticDir string
	baseURL   string // configured public origin; "" = derive per request
}

// New parses templates and returns a Web. staticDir is the on-disk directory
// holding the compiled CSS + vendored JS (built by `make css`, see web/);
// empty disables static serving (unstyled pages — tests don't care). baseURL
// is the configured public origin (PUBLIC_BASE_URL) used for canonical URLs;
// when empty it's derived from each request's forwarded headers.
func New(s *store.Store, staticDir, baseURL string) (*Web, error) {
	t, err := template.New("").Funcs(template.FuncMap{
		"humanizeAgo":      humanizeAgo,
		"qualityStars":     qualityStars,
		"qualityColor":     qualityColor,
		"qualityPaletteJS": qualityPaletteJS,
	}).ParseFS(tmplFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	return &Web{store: s, tmpl: t, staticDir: staticDir, baseURL: strings.TrimRight(baseURL, "/")}, nil
}

// publicBase returns the origin for canonical URLs and the sitemap (see
// httpx.PublicBase).
func (web *Web) publicBase(r *http.Request) string {
	return httpx.PublicBase(r, web.baseURL)
}

// humanizeAgo turns a unix-millisecond timestamp into a short relative string
// ("3 min ago", "2 h ago", "5 days ago"). Accepts a *int64 so Spot's nullable
// LastConfirmedAt can be passed directly from a template.
func humanizeAgo(ms *int64) string {
	if ms == nil || *ms <= 0 {
		return ""
	}
	d := time.Since(time.UnixMilli(*ms))
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	default:
		return fmt.Sprintf("%d months ago", int(d.Hours()/(24*30)))
	}
}

// qualityStars renders a manual quality rating (1-3) as filled/empty stars;
// "" for unrated so the template can omit the row entirely.
func qualityStars(q int) string {
	if q < 1 || q > 3 {
		return ""
	}
	return strings.Repeat("★", q) + strings.Repeat("☆", 3-q)
}

// qualityColors is the single source of truth for the quality palette, shared
// by the server-rendered share page (qualityColor) and the landing-page client
// JS (qualityPaletteJS) so the two never drift. 0=unrated, 1=basic … 3=great.
var qualityColors = map[int]string{0: "#9ca3af", 1: "#dc2626", 2: "#f59e0b", 3: "#16a34a"}

// qualityColor maps a quality rating to its pin/badge colour.
func qualityColor(q int) string {
	if c, ok := qualityColors[q]; ok {
		return c
	}
	return qualityColors[0]
}

// qualityPaletteJS renders qualityColors as a JS object literal for the landing
// template, keeping the client palette in lock-step with the server's.
func qualityPaletteJS() template.JS {
	b, _ := json.Marshal(qualityColors)
	return template.JS(b)
}

// Routes registers the public web routes.
func (web *Web) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", web.landing) // exact "/" only
	mux.HandleFunc("GET /s/{id}", web.share)
	mux.HandleFunc("GET /reset", web.reset)
	mux.HandleFunc("GET /verify", web.verify)
	mux.HandleFunc("GET /sitemap.xml", web.sitemap)
	mux.HandleFunc("GET /robots.txt", web.robots)
	// Moderation console. The page is public HTML but every action behind it
	// hits the admin-gated API with the operator's bearer token.
	mux.HandleFunc("GET /admin", func(w http.ResponseWriter, r *http.Request) {
		web.render(w, "admin.html", nil)
	})
	if web.staticDir != "" {
		fs := http.StripPrefix("/static/", http.FileServer(http.Dir(web.staticDir)))
		mux.Handle("GET /static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Compiled assets change only on deploy; an hour of caching keeps
			// repeat visits fast without long-lived stale CSS after a release.
			w.Header().Set("Cache-Control", "public, max-age=3600")
			fs.ServeHTTP(w, r)
		}))
		// The service worker must live at the origin root (scope "/") and must
		// never be cached long, or deploys take days to propagate.
		mux.HandleFunc("GET /sw.js", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			http.ServeFile(w, r, web.staticDir+"/sw.js")
		})
		mux.HandleFunc("GET /manifest.webmanifest", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Content-Type", "application/manifest+json")
			http.ServeFile(w, r, web.staticDir+"/manifest.webmanifest")
		})
	}
}

// verify renders the email-verification landing page for a signup magic link;
// the page POSTs the token to /api/auth/verify-email on load.
func (web *Web) verify(w http.ResponseWriter, r *http.Request) {
	web.render(w, "verify.html", struct{ Token string }{Token: r.URL.Query().Get("token")})
}

func (web *Web) landing(w http.ResponseWriter, r *http.Request) {
	web.render(w, "landing.html", struct{ Canonical string }{Canonical: web.publicBase(r) + "/"})
}

// shareData is share.html's view model: the spot plus the absolute URLs and
// pre-marshaled JSON-LD the SEO tags need.
type shareData struct {
	*models.Spot
	Canonical string
	OGImage   string
	JSONLD    template.JS
}

// shareJSONLD builds the schema.org Place payload for a spot. Marshaled in Go
// (not assembled in the template) and "</" -escaped so a malicious venue name
// can't break out of the <script> tag.
func shareJSONLD(sp *models.Spot, canonical string) template.JS {
	name := sp.VenueName
	if name == "" {
		name = sp.ESSID
	}
	doc := map[string]any{
		"@context": "https://schema.org",
		"@type":    "Place",
		"name":     name,
		"url":      canonical,
		"geo": map[string]any{
			"@type":     "GeoCoordinates",
			"latitude":  sp.Lat,
			"longitude": sp.Lng,
		},
		"amenityFeature": []map[string]any{{
			"@type": "LocationFeatureSpecification",
			"name":  "Free WiFi",
			"value": true,
		}},
		"publicAccess": true,
	}
	b, _ := json.Marshal(doc)
	return template.JS(strings.ReplaceAll(string(b), "</", `<\/`))
}

// reset renders the password-reset page for a magic link. The token from the
// query string is passed through to the template (which POSTs it back to
// /api/auth/reset-password along with the new password).
func (web *Web) reset(w http.ResponseWriter, r *http.Request) {
	web.render(w, "reset.html", struct{ Token string }{Token: r.URL.Query().Get("token")})
}

func (web *Web) share(w http.ResponseWriter, r *http.Request) {
	sp, err := web.store.GetSpot(r.Context(), r.PathValue("id"), "")
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "spot not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	base := web.publicBase(r)
	canonical := base + "/s/" + sp.ID
	web.render(w, "share.html", shareData{
		Spot:      sp,
		Canonical: canonical,
		OGImage:   base + "/static/icons/icon-512.png",
		JSONLD:    shareJSONLD(sp, canonical),
	})
}

// sitemap lists the landing page plus every spot's share page. Single urlset:
// the 50k-URL spec limit is far away at this scale (revisit with an index
// file if the directory ever approaches it).
func (web *Web) sitemap(w http.ResponseWriter, r *http.Request) {
	entries, err := web.store.SpotsForSitemap(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	base := web.publicBase(r)
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	_, _ = fmt.Fprintf(w, "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n"+
		"<urlset xmlns=\"http://www.sitemaps.org/schemas/sitemap/0.9\">\n")
	_, _ = fmt.Fprintf(w, "  <url><loc>%s</loc></url>\n", xmlEscape(base+"/"))
	for _, e := range entries {
		_, _ = fmt.Fprintf(w, "  <url><loc>%s</loc><lastmod>%s</lastmod></url>\n",
			xmlEscape(base+"/s/"+e.ID), time.UnixMilli(e.UpdatedAt).UTC().Format("2006-01-02"))
	}
	_, _ = fmt.Fprintf(w, "</urlset>\n")
}

// robots allows everything except the API and the magic-link pages, blocks a
// few low-signal scrapers, and advertises the sitemap on the real host.
func (web *Web) robots(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = fmt.Fprintf(w, `User-agent: *
Disallow: /api/
Disallow: /reset
Disallow: /verify
Disallow: /admin
Allow: /

User-agent: Bytespider
Disallow: /
User-agent: Diffbot
Disallow: /
User-agent: DataForSeoBot
Disallow: /
User-agent: PetalBot
Disallow: /

Sitemap: %s/sitemap.xml
`, web.publicBase(r))
}

// xmlEscape escapes the five XML special characters (and only those — never
// use an HTML-entity encoder for XML).
func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return r.Replace(s)
}

func (web *Web) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := web.tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}
