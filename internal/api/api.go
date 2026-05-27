// Package api implements the JSON HTTP API for WiFi Spots.
//
// Browsing (nearby/area/get) is anonymous. Mutations (create/update/delete)
// require a bearer-token session; edit/delete additionally require ownership
// (or admin). No list endpoint truncates silently: nearby is radius-capped with
// a documented limit, and area is fully cursor-paginated.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/oriolj/wifi_psw_sharer/internal/auth"
	"github.com/oriolj/wifi_psw_sharer/internal/models"
	"github.com/oriolj/wifi_psw_sharer/internal/store"
)

const (
	sessionTTL       = 30 * 24 * time.Hour
	nearbyMaxRadius  = 50.0  // km — interactive
	areaMaxRadius    = 300.0 // km — CLI bulk download
	nearbyResultsCap = 200   // documented hard cap for the radius-bounded nearby list
	areaPageSize     = 200   // cursor page size for area
)

type ctxKey int

const userCtxKey ctxKey = 0

// API is the HTTP handler set backed by a Store.
type API struct {
	store     *store.Store
	allowCORS bool
	log       *slog.Logger
	dummyHash string // verified against on unknown-user login to equalize timing
}

// New returns an API. allowCORS enables permissive CORS for local dev.
func New(s *store.Store, allowCORS bool, log *slog.Logger) *API {
	if log == nil {
		log = slog.Default()
	}
	// Precompute a hash so the login path spends the same argon2 time whether or
	// not the username exists (defeats username enumeration via timing).
	dummy, _ := auth.HashPassword("timing-equalizer-not-a-real-password")
	return &API{store: s, allowCORS: allowCORS, log: log, dummyHash: dummy}
}

// Routes registers the API routes on the given mux under /api/.
func (a *API) Routes(mux *http.ServeMux) {
	h := func(f http.HandlerFunc) http.HandlerFunc { return a.withUser(f) }

	mux.HandleFunc("GET /api/health", a.health)
	mux.HandleFunc("POST /api/auth/register", a.register)
	mux.HandleFunc("POST /api/auth/login", a.login)
	mux.HandleFunc("POST /api/auth/logout", h(a.logout))

	mux.HandleFunc("GET /api/spots/nearby", a.nearby)
	mux.HandleFunc("GET /api/spots/area", a.area)
	mux.HandleFunc("GET /api/spots/{id}", a.getSpot)
	mux.HandleFunc("POST /api/spots", h(a.createSpot))
	mux.HandleFunc("PUT /api/spots/{id}", h(a.updateSpot))
	mux.HandleFunc("DELETE /api/spots/{id}", h(a.deleteSpot))
	mux.HandleFunc("POST /api/spots/{id}/report", h(a.reportSpot))
}

// Middleware wraps a handler with optional-CORS handling.
func (a *API) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.allowCORS {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// withUser loads the bearer-token user into the request context if present.
func (a *API) withUser(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token != "" {
			if u, err := a.store.UserForToken(r.Context(), token); err == nil {
				r = r.WithContext(context.WithValue(r.Context(), userCtxKey, u))
			}
		}
		next(w, r)
	}
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return ""
}

func userFrom(ctx context.Context) (*models.User, bool) {
	u, ok := ctx.Value(userCtxKey).(*models.User)
	return u, ok
}

// requireUser returns the authed user or writes 401 and returns false.
func (a *API) requireUser(w http.ResponseWriter, r *http.Request) (*models.User, bool) {
	u, ok := userFrom(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "authentication required")
		return nil, false
	}
	return u, true
}

// ---- handlers ----

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type authReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}
type authResp struct {
	Token string       `json:"token"`
	User  *models.User `json:"user"`
}

func (a *API) register(w http.ResponseWriter, r *http.Request) {
	var req authReq
	if !decode(w, r, &req) {
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if len(req.Username) < 3 || len(req.Password) < 8 {
		writeErr(w, http.StatusBadRequest, "username must be ≥3 chars and password ≥8 chars")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	u, err := a.store.CreateUser(r.Context(), req.Username, hash)
	if errors.Is(err, store.ErrUsernameTaken) {
		writeErr(w, http.StatusConflict, "username already taken")
		return
	}
	if err != nil {
		a.serverErr(w, err)
		return
	}
	a.issueToken(r.Context(), w, u)
}

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	var req authReq
	if !decode(w, r, &req) {
		return
	}
	u, err := a.store.GetUserByUsername(r.Context(), strings.TrimSpace(req.Username))
	if err != nil {
		// Run a verify against a dummy hash anyway so an unknown username costs
		// the same wall-clock time as a known one (no enumeration side channel).
		_, _ = auth.VerifyPassword(req.Password, a.dummyHash)
		writeErr(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	ok, err := auth.VerifyPassword(req.Password, u.PasswordHash)
	if err != nil || !ok {
		writeErr(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	a.issueToken(r.Context(), w, u)
}

func (a *API) issueToken(ctx context.Context, w http.ResponseWriter, u *models.User) {
	sess, err := a.store.CreateSession(ctx, u.ID, sessionTTL)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, authResp{Token: sess.Token, User: u})
}

func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	if token := bearerToken(r); token != "" {
		_ = a.store.DeleteSession(r.Context(), token)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) nearby(w http.ResponseWriter, r *http.Request) {
	lat, lng, ok := latLng(w, r)
	if !ok {
		return
	}
	radius := floatParam(r, "radius_km", 5)
	if radius > nearbyMaxRadius {
		radius = nearbyMaxRadius
	}
	spots, truncated, err := a.store.Nearby(r.Context(), lat, lng, radius, nearbyResultsCap)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"results": nonNil(spots),
		"count":   len(spots),
		"capped":  truncated,
	})
}

func (a *API) area(w http.ResponseWriter, r *http.Request) {
	lat, lng, ok := latLng(w, r)
	if !ok {
		return
	}
	radius := floatParam(r, "radius_km", 200)
	if radius > areaMaxRadius {
		radius = areaMaxRadius
	}
	cursor := r.URL.Query().Get("cursor")
	spots, next, err := a.store.Area(r.Context(), lat, lng, radius, cursor, areaPageSize)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"results":     nonNil(spots),
		"next_cursor": next,
	})
}

func (a *API) getSpot(w http.ResponseWriter, r *http.Request) {
	sp, err := a.store.GetSpot(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "spot not found")
		return
	}
	if err != nil {
		a.serverErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sp)
}

type spotReq struct {
	VenueName string   `json:"venue_name"`
	ESSID     string   `json:"essid"`
	Password  string   `json:"password"`
	AuthType  string   `json:"auth_type"`
	Lat       *float64 `json:"lat"`
	Lng       *float64 `json:"lng"`
	Notes     string   `json:"notes"`
	PingMS    *int     `json:"ping_ms"`
	DownMbps  *float64 `json:"down_mbps"`
	UpMbps    *float64 `json:"up_mbps"`
}

func (req *spotReq) apply(sp *models.Spot) string {
	if strings.TrimSpace(req.ESSID) == "" {
		return "essid is required"
	}
	if req.Lat == nil || req.Lng == nil {
		return "lat and lng are required"
	}
	if *req.Lat < -90 || *req.Lat > 90 || *req.Lng < -180 || *req.Lng > 180 {
		return "lat/lng out of range"
	}
	authType := req.AuthType
	if authType == "" {
		authType = "wpa2"
	}
	if !models.ValidAuthTypes[authType] {
		return "auth_type must be one of wpa2, wpa3, wep, open"
	}
	sp.VenueName = strings.TrimSpace(req.VenueName)
	sp.ESSID = req.ESSID
	sp.Password = req.Password
	sp.AuthType = authType
	sp.Lat = *req.Lat
	sp.Lng = *req.Lng
	sp.Notes = req.Notes
	sp.PingMS = req.PingMS
	sp.DownMbps = req.DownMbps
	sp.UpMbps = req.UpMbps
	return ""
}

func (a *API) createSpot(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	var req spotReq
	if !decode(w, r, &req) {
		return
	}
	sp := &models.Spot{CreatedBy: u.ID}
	if msg := req.apply(sp); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	created, err := a.store.CreateSpot(r.Context(), sp)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (a *API) updateSpot(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	sp, err := a.store.GetSpot(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "spot not found")
		return
	}
	if err != nil {
		a.serverErr(w, err)
		return
	}
	if sp.CreatedBy != u.ID && !u.IsAdmin {
		writeErr(w, http.StatusForbidden, "you can only edit your own spots")
		return
	}
	var req spotReq
	if !decode(w, r, &req) {
		return
	}
	if msg := req.apply(sp); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if err := a.store.UpdateSpot(r.Context(), sp); err != nil {
		a.serverErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sp)
}

func (a *API) deleteSpot(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	sp, err := a.store.GetSpot(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "spot not found")
		return
	}
	if err != nil {
		a.serverErr(w, err)
		return
	}
	if sp.CreatedBy != u.ID && !u.IsAdmin {
		writeErr(w, http.StatusForbidden, "you can only delete your own spots")
		return
	}
	if err := a.store.DeleteSpot(r.Context(), sp.ID); err != nil {
		a.serverErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) reportSpot(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if !decode(w, r, &req) {
		return
	}
	if !models.ValidReportReasons[req.Reason] {
		writeErr(w, http.StatusBadRequest, "reason must be wrong_password, gone, spam, or other")
		return
	}
	if _, err := a.store.GetSpot(r.Context(), r.PathValue("id")); errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "spot not found")
		return
	}
	if _, err := a.store.CreateReport(r.Context(), r.PathValue("id"), req.Reason, u.ID); err != nil {
		a.serverErr(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// ---- helpers ----

func latLng(w http.ResponseWriter, r *http.Request) (lat, lng float64, ok bool) {
	q := r.URL.Query()
	var err1, err2 error
	lat, err1 = strconv.ParseFloat(q.Get("lat"), 64)
	lng, err2 = strconv.ParseFloat(q.Get("lng"), 64)
	if err1 != nil || err2 != nil {
		writeErr(w, http.StatusBadRequest, "lat and lng query params are required")
		return 0, 0, false
	}
	return lat, lng, true
}

func floatParam(r *http.Request, name string, def float64) float64 {
	if v, err := strconv.ParseFloat(r.URL.Query().Get(name), 64); err == nil && v > 0 {
		return v
	}
	return def
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (a *API) serverErr(w http.ResponseWriter, err error) {
	a.log.Error("server error", "err", err)
	writeErr(w, http.StatusInternalServerError, "internal server error")
}

// nonNil returns an empty slice instead of nil so JSON renders [] not null.
func nonNil(s []*models.Spot) []*models.Spot {
	if s == nil {
		return []*models.Spot{}
	}
	return s
}
