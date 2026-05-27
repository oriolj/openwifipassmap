// Package web serves the small public, server-rendered site: a landing page
// that lists nearby spots (via browser geolocation) and a shareable per-spot
// page. This is the "public web meant to share" surface; the richer experience
// lives in the Capacitor mobile app.
package web

import (
	"embed"
	"errors"
	"html/template"
	"net/http"

	"github.com/oriolj/wifi_psw_sharer/internal/store"
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
	t, err := template.ParseFS(tmplFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	return &Web{store: s, tmpl: t}, nil
}

// Routes registers the public web routes.
func (web *Web) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", web.landing) // exact "/" only
	mux.HandleFunc("GET /s/{id}", web.share)
}

func (web *Web) landing(w http.ResponseWriter, r *http.Request) {
	web.render(w, "landing.html", nil)
}

func (web *Web) share(w http.ResponseWriter, r *http.Request) {
	sp, err := web.store.GetSpot(r.Context(), r.PathValue("id"))
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
