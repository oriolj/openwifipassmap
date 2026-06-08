// Package web serves the small public, server-rendered site: a landing page
// that lists nearby spots (via browser geolocation) and a shareable per-spot
// page. This is the "public web meant to share" surface; the richer experience
// lives in the Capacitor mobile app.
package web

import (
	"embed"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"time"

	"github.com/oriolj/openwifipassmap/internal/store"
)

//go:embed templates/*.html
var tmplFS embed.FS

// Web renders the public site.
type Web struct {
	store *store.Store
	tmpl  *template.Template
}

// New parses templates and returns a Web.
func New(s *store.Store) (*Web, error) {
	t, err := template.New("").Funcs(template.FuncMap{
		"humanizeAgo": humanizeAgo,
	}).ParseFS(tmplFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	return &Web{store: s, tmpl: t}, nil
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

// Routes registers the public web routes.
func (web *Web) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", web.landing) // exact "/" only
	mux.HandleFunc("GET /s/{id}", web.share)
	mux.HandleFunc("GET /reset", web.reset)
}

func (web *Web) landing(w http.ResponseWriter, r *http.Request) {
	web.render(w, "landing.html", nil)
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
	web.render(w, "share.html", sp)
}

func (web *Web) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := web.tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}
