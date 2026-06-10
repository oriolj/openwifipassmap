// Package api implements the JSON HTTP API for OpenWifiPassMap.
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
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/oriolj/openwifipassmap/internal/auth"
	"github.com/oriolj/openwifipassmap/internal/email"
	"github.com/oriolj/openwifipassmap/internal/models"
	"github.com/oriolj/openwifipassmap/internal/store"
)

const (
	sessionTTL       = 30 * 24 * time.Hour
	passwordResetTTL = time.Hour      // magic-link lifetime
	emailVerifyTTL   = 48 * time.Hour // verification-link lifetime
	nearbyMaxRadius  = 50.0           // km — interactive
	areaMaxRadius    = 300.0          // km — CLI bulk download
	nearbyResultsCap = 200            // documented hard cap for the radius-bounded nearby list
	areaPageSize     = 200            // cursor page size for area
	reportsCap       = 500            // admin reports list cap (total returned alongside)
)

type ctxKey int

const userCtxKey ctxKey = 0

// API is the HTTP handler set backed by a Store.
type API struct {
	store     *store.Store
	allowCORS bool
	log       *slog.Logger
	dummyHash string // verified against on unknown-user login to equalize timing
	mailer    email.Sender
	baseURL   string // public origin for links in emails, no trailing slash
}

// New returns an API. allowCORS enables permissive CORS for local dev. mailer
// sends transactional email (password resets); baseURL is the public origin
// used to build links in those emails.
func New(s *store.Store, allowCORS bool, log *slog.Logger, mailer email.Sender, baseURL string) *API {
	if log == nil {
		log = slog.Default()
	}
	if mailer == nil {
		mailer = email.New("", "", log) // logging fallback
	}
	// Precompute a hash so the login path spends the same argon2 time whether or
	// not the username exists (defeats username enumeration via timing).
	dummy, _ := auth.HashPassword("timing-equalizer-not-a-real-password")
	return &API{
		store:     s,
		allowCORS: allowCORS,
		log:       log,
		dummyHash: dummy,
		mailer:    mailer,
		baseURL:   strings.TrimRight(baseURL, "/"),
	}
}

// Routes registers the API routes on the given mux under /api/.
func (a *API) Routes(mux *http.ServeMux) {
	h := func(f http.HandlerFunc) http.HandlerFunc { return a.withUser(f) }

	// Abuse brakes on the credential endpoints: generous for interactive
	// login/register (argon2 already makes guessing expensive), tight for
	// forgot-password since every allowed request can send an email.
	authRL := newRateLimiter(20, 2*time.Second)  // 20 burst, 30/min sustained
	forgotRL := newRateLimiter(3, 5*time.Minute) // 3 burst, then 1 per 5 min
	resendRL := newRateLimiter(3, 5*time.Minute) // own bucket, also sends mail
	limited := func(rl *rateLimiter, f http.HandlerFunc) http.HandlerFunc { return a.rateLimit(rl, f) }

	mux.HandleFunc("GET /api/health", a.health)
	mux.HandleFunc("POST /api/auth/register", limited(authRL, a.register))
	mux.HandleFunc("POST /api/auth/login", limited(authRL, a.login))
	mux.HandleFunc("POST /api/auth/logout", h(a.logout))
	mux.HandleFunc("POST /api/auth/forgot-password", limited(forgotRL, a.forgotPassword))
	mux.HandleFunc("POST /api/auth/reset-password", limited(authRL, a.resetPassword))
	mux.HandleFunc("POST /api/auth/verify-email", limited(authRL, a.verifyEmail))
	mux.HandleFunc("POST /api/auth/resend-verification", limited(resendRL, h(a.resendVerification)))
	mux.HandleFunc("GET /api/me", h(a.me))
	mux.HandleFunc("GET /api/me/spots", h(a.mySpots))
	mux.HandleFunc("POST /api/me/password", limited(authRL, h(a.changePassword)))
	mux.HandleFunc("POST /api/me/email", limited(authRL, h(a.changeEmail)))
	mux.HandleFunc("GET /api/reports", h(a.listReports))
	mux.HandleFunc("DELETE /api/reports/{id}", h(a.deleteReport))

	// Reads are anonymous but auth-aware: with a bearer token they also carry
	// viewer-specific fields (confirmed_by_me, my_rating).
	mux.HandleFunc("GET /api/spots/nearby", h(a.nearby))
	mux.HandleFunc("GET /api/spots/area", h(a.area))
	mux.HandleFunc("GET /api/spots/{id}", h(a.getSpot))
	mux.HandleFunc("POST /api/spots", h(a.createSpot))
	mux.HandleFunc("PUT /api/spots/{id}", h(a.updateSpot))
	mux.HandleFunc("DELETE /api/spots/{id}", h(a.deleteSpot))
	mux.HandleFunc("POST /api/spots/{id}/report", h(a.reportSpot))
	mux.HandleFunc("POST /api/spots/{id}/confirm", h(a.confirmSpot))
	mux.HandleFunc("POST /api/spots/{id}/review", h(a.reviewSpot))
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

// viewerID returns the authed user's id, or "" if the request is anonymous.
// Used to ask the store for "confirmed_by_me" without requiring auth.
func viewerID(ctx context.Context) string {
	if u, ok := userFrom(ctx); ok {
		return u.ID
	}
	return ""
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
	Email    string `json:"email"`
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
	addr, err := normalizeEmail(req.Email)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "a valid email address is required")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	u, err := a.store.CreateUser(r.Context(), req.Username, addr, hash)
	if errors.Is(err, store.ErrUsernameTaken) {
		writeErr(w, http.StatusConflict, "username already taken")
		return
	}
	if err != nil {
		a.serverErr(w, err)
		return
	}
	// Verification is non-blocking: the account works right away, but password
	// reset only emails verified addresses, so the link matters.
	if token, err := a.store.CreateEmailVerificationToken(r.Context(), u.ID, emailVerifyTTL); err != nil {
		a.log.Error("create verification token", "err", err, "user", u.ID)
	} else {
		a.sendVerificationEmail(addr, u.Username, a.publicBase(r)+"/verify?token="+url.QueryEscape(token))
	}
	a.issueToken(r.Context(), w, u)
}

// verifyEmail consumes an email-verification token (from the signup email's
// magic link) and marks the account's address verified.
func (a *API) verifyEmail(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &req) {
		return
	}
	_, err := a.store.ConsumeEmailVerificationToken(r.Context(), strings.TrimSpace(req.Token))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusBadRequest, "this verification link is invalid or has expired")
		return
	}
	if err != nil {
		a.serverErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "Email verified — thanks!"})
}

// normalizeEmail validates and canonicalizes an email address. It returns the
// trimmed address or an error if it isn't a syntactically valid single address.
func normalizeEmail(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("empty email")
	}
	addr, err := mail.ParseAddress(raw)
	if err != nil {
		return "", err
	}
	return addr.Address, nil
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

// me returns the authenticated user plus lightweight account stats (how many
// spots they've contributed). Also serves as the "who am I" lookup.
func (a *API) me(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	n, err := a.store.CountSpotsByUser(r.Context(), u.ID)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": u, "spots_added": n})
}

// listReports returns the newest moderation reports (admin only). The response
// carries total alongside results so a capped list is never a silent truncation.
func (a *API) listReports(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	if !u.IsAdmin {
		writeErr(w, http.StatusForbidden, "admin only")
		return
	}
	reports, total, err := a.store.ListReports(r.Context(), reportsCap)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	if reports == nil {
		reports = []*models.Report{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": reports, "total": total})
}

// mySpots lists the authed user's own spots, cursor-paginated like area.
func (a *API) mySpots(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	spots, next, err := a.store.SpotsByUser(r.Context(), u.ID, r.URL.Query().Get("cursor"), areaPageSize)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"results":     nonNil(spots),
		"next_cursor": next,
	})
}

// changePassword sets a new password for the authed user after re-verifying
// the current one, then logs out every OTHER session (devices with a possibly
// compromised credential) while keeping this one alive.
func (a *API) changePassword(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if !decode(w, r, &req) {
		return
	}
	if len(req.NewPassword) < 8 {
		writeErr(w, http.StatusBadRequest, "new password must be ≥8 chars")
		return
	}
	if ok, err := auth.VerifyPassword(req.CurrentPassword, u.PasswordHash); err != nil || !ok {
		writeErr(w, http.StatusForbidden, "current password is incorrect")
		return
	}
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	if err := a.store.UpdateUserPassword(r.Context(), u.ID, hash); err != nil {
		a.serverErr(w, err)
		return
	}
	if err := a.store.DeleteUserSessionsExcept(r.Context(), u.ID, bearerToken(r)); err != nil {
		a.log.Error("revoke other sessions", "err", err, "user", u.ID)
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "Password updated. Other devices were logged out."})
}

// changeEmail sets a new address (password re-verified), resets the verified
// flag, and sends a fresh verification link to the new address.
func (a *API) changeEmail(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	var req struct {
		Password string `json:"password"`
		Email    string `json:"email"`
	}
	if !decode(w, r, &req) {
		return
	}
	addr, err := normalizeEmail(req.Email)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "a valid email address is required")
		return
	}
	if ok, err := auth.VerifyPassword(req.Password, u.PasswordHash); err != nil || !ok {
		writeErr(w, http.StatusForbidden, "password is incorrect")
		return
	}
	if err := a.store.UpdateUserEmail(r.Context(), u.ID, addr); err != nil {
		a.serverErr(w, err)
		return
	}
	if token, err := a.store.CreateEmailVerificationToken(r.Context(), u.ID, emailVerifyTTL); err != nil {
		a.log.Error("create verification token", "err", err, "user", u.ID)
	} else {
		a.sendVerificationEmail(addr, u.Username, a.publicBase(r)+"/verify?token="+url.QueryEscape(token))
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "Email updated — check your inbox for the verification link."})
}

// resendVerification sends a fresh verification link for the authed user's
// current (still unverified) address.
func (a *API) resendVerification(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	if u.EmailVerified {
		writeErr(w, http.StatusBadRequest, "this email is already verified")
		return
	}
	token, err := a.store.CreateEmailVerificationToken(r.Context(), u.ID, emailVerifyTTL)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	a.sendVerificationEmail(u.Email, u.Username, a.publicBase(r)+"/verify?token="+url.QueryEscape(token))
	writeJSON(w, http.StatusOK, map[string]string{"message": "Verification email sent — check your inbox."})
}

// deleteReport dismisses a moderation report (admin only).
func (a *API) deleteReport(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	if !u.IsAdmin {
		writeErr(w, http.StatusForbidden, "admin only")
		return
	}
	err := a.store.DeleteReport(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "report not found")
		return
	}
	if err != nil {
		a.serverErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// forgotPassword issues a password-reset magic link for every account under the
// submitted email. It always returns 200 with a generic message — never
// revealing whether the address has an account — so it can't be used to
// enumerate registered emails. Mail is sent asynchronously so a slow/ failing
// SMTP provider can't stall or time the response.
func (a *API) forgotPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
	}
	if !decode(w, r, &req) {
		return
	}
	const generic = "If that email has an account, a reset link is on its way."
	addr, err := normalizeEmail(req.Email)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]string{"message": generic})
		return
	}
	users, err := a.store.GetUsersByEmail(r.Context(), addr)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	base := a.publicBase(r)
	for _, u := range users {
		// Only verified addresses get reset mail: an attacker who registered
		// someone else's email can't use the reset flow to land mail in (or
		// take over via) an inbox that never confirmed the account.
		if !u.EmailVerified {
			continue
		}
		token, err := a.store.CreatePasswordResetToken(r.Context(), u.ID, passwordResetTTL)
		if err != nil {
			a.log.Error("create reset token", "err", err, "user", u.ID)
			continue
		}
		link := base + "/reset?token=" + url.QueryEscape(token)
		a.sendResetEmail(addr, u.Username, link)
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": generic})
}

// publicBase returns the origin to use for links in emails. A configured
// baseURL (PUBLIC_BASE_URL) is authoritative; otherwise it's derived from the
// incoming request so links use whatever host the user actually hit — including
// behind a TLS-terminating proxy like Coolify's Traefik (X-Forwarded-Proto/Host).
// Set PUBLIC_BASE_URL in prod to be immune to Host-header spoofing.
func (a *API) publicBase(r *http.Request) string {
	if a.baseURL != "" {
		return a.baseURL
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

// sendMailAsync dispatches an email on a background goroutine so the request
// returns immediately regardless of the mail provider's latency.
func (a *API) sendMailAsync(to, subject, html, text string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := a.mailer.Send(ctx, to, subject, html, text); err != nil {
			a.log.Error("send email", "err", err, "to", to, "subject", subject)
		}
	}()
}

func (a *API) sendResetEmail(to, username, link string) {
	text := fmt.Sprintf("Hi %s,\n\nWe got a request to reset your OpenWifiPassMap password. "+
		"Open this link to choose a new one:\n\n%s\n\n"+
		"This link expires in 1 hour. If you didn't ask for this, you can ignore this email.",
		username, link)
	html := fmt.Sprintf(`<p>Hi %s,</p>`+
		`<p>We got a request to reset your OpenWifiPassMap password. `+
		`Click the button below to choose a new one:</p>`+
		`<p><a href="%s">Reset my password</a></p>`+
		`<p>This link expires in 1 hour. If you didn't ask for this, you can ignore this email.</p>`,
		template.HTMLEscapeString(username), template.HTMLEscapeString(link))
	a.sendMailAsync(to, "Reset your OpenWifiPassMap password", html, text)
}

func (a *API) sendVerificationEmail(to, username, link string) {
	text := fmt.Sprintf("Hi %s,\n\nWelcome to OpenWifiPassMap! Confirm this email address "+
		"so you can recover your account later:\n\n%s\n\n"+
		"This link expires in 48 hours. If you didn't create this account, you can ignore this email.",
		username, link)
	html := fmt.Sprintf(`<p>Hi %s,</p>`+
		`<p>Welcome to OpenWifiPassMap! Confirm this email address so you can recover your account later:</p>`+
		`<p><a href="%s">Verify my email</a></p>`+
		`<p>This link expires in 48 hours. If you didn't create this account, you can ignore this email.</p>`,
		template.HTMLEscapeString(username), template.HTMLEscapeString(link))
	a.sendMailAsync(to, "Verify your OpenWifiPassMap email", html, text)
}

// resetPassword consumes a single-use reset token and sets a new password,
// then revokes the user's existing sessions so any old/stolen token is dead.
func (a *API) resetPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if !decode(w, r, &req) {
		return
	}
	if len(req.Password) < 8 {
		writeErr(w, http.StatusBadRequest, "password must be ≥8 chars")
		return
	}
	userID, err := a.store.ConsumePasswordResetToken(r.Context(), strings.TrimSpace(req.Token))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusBadRequest, "this reset link is invalid or has expired")
		return
	}
	if err != nil {
		a.serverErr(w, err)
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	if err := a.store.UpdateUserPassword(r.Context(), userID, hash); err != nil {
		a.serverErr(w, err)
		return
	}
	if err := a.store.DeleteUserSessions(r.Context(), userID); err != nil {
		a.log.Error("revoke sessions after reset", "err", err, "user", userID)
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "Password updated — you can now log in."})
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
	spots, truncated, err := a.store.Nearby(r.Context(), lat, lng, radius, nearbyResultsCap, viewerID(r.Context()))
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
	spots, next, err := a.store.Area(r.Context(), lat, lng, radius, cursor, areaPageSize, viewerID(r.Context()))
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
	sp, err := a.store.GetSpot(r.Context(), r.PathValue("id"), viewerID(r.Context()))
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
	Quality   *int     `json:"quality"`
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
	if req.Quality != nil && (*req.Quality < 0 || *req.Quality > 3) {
		return "quality must be between 0 (unrated) and 3"
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
	if req.Quality != nil {
		sp.Quality = *req.Quality
	}
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

// loadOwnedSpot fetches the spot at the request's {id} path value and verifies
// u may mutate it (owner or admin). It writes the matching error response and
// returns ok=false on miss/forbidden; action names the verb for the 403 message.
func (a *API) loadOwnedSpot(w http.ResponseWriter, r *http.Request, u *models.User, action string) (*models.Spot, bool) {
	sp, err := a.store.GetSpot(r.Context(), r.PathValue("id"), u.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "spot not found")
		return nil, false
	}
	if err != nil {
		a.serverErr(w, err)
		return nil, false
	}
	if sp.CreatedBy != u.ID && !u.IsAdmin {
		writeErr(w, http.StatusForbidden, "you can only "+action+" your own spots")
		return nil, false
	}
	return sp, true
}

func (a *API) updateSpot(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	sp, ok := a.loadOwnedSpot(w, r, u, "edit")
	if !ok {
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
	// Quality/speed in a PUT body are the owner's review, not spot facts —
	// route them through the reviews aggregate like everyone else's.
	if req.Quality != nil || req.DownMbps != nil || req.UpMbps != nil || req.PingMS != nil {
		if err := a.store.UpsertReview(r.Context(), sp.ID, u.ID, req.Quality, req.DownMbps, req.UpMbps, req.PingMS); err != nil {
			a.serverErr(w, err)
			return
		}
	}
	fresh, err := a.store.GetSpot(r.Context(), sp.ID, u.ID)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, fresh)
}

// reviewSpot records the authed user's rating (1-3) and/or speed measurement
// for any spot — their own or someone else's — and returns the spot with
// recomputed aggregates.
func (a *API) reviewSpot(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	var req struct {
		Quality  *int     `json:"quality"`
		DownMbps *float64 `json:"down_mbps"`
		UpMbps   *float64 `json:"up_mbps"`
		PingMS   *int     `json:"ping_ms"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Quality == nil && req.DownMbps == nil && req.UpMbps == nil && req.PingMS == nil {
		writeErr(w, http.StatusBadRequest, "provide a quality rating and/or a speed measurement")
		return
	}
	if req.Quality != nil && (*req.Quality < 1 || *req.Quality > 3) {
		writeErr(w, http.StatusBadRequest, "quality must be between 1 and 3")
		return
	}
	spotID := r.PathValue("id")
	err := a.store.UpsertReview(r.Context(), spotID, u.ID, req.Quality, req.DownMbps, req.UpMbps, req.PingMS)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "spot not found")
		return
	}
	if err != nil {
		a.serverErr(w, err)
		return
	}
	sp, err := a.store.GetSpot(r.Context(), spotID, u.ID)
	if err != nil {
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
	sp, ok := a.loadOwnedSpot(w, r, u, "delete")
	if !ok {
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
	if _, err := a.store.GetSpot(r.Context(), r.PathValue("id"), u.ID); errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "spot not found")
		return
	}
	if _, err := a.store.CreateReport(r.Context(), r.PathValue("id"), req.Reason, u.ID); err != nil {
		a.serverErr(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// confirmSpot records that the authed user attests this spot's credentials
// still work. Returns the spot with updated confirmation stats so the client
// doesn't need a follow-up GET. The store's upsert keeps re-confirms cheap.
func (a *API) confirmSpot(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	spotID := r.PathValue("id")
	err := a.store.CreateConfirmation(r.Context(), spotID, u.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "spot not found")
		return
	}
	if errors.Is(err, store.ErrSelfConfirm) {
		writeErr(w, http.StatusForbidden, "you cannot confirm your own spot")
		return
	}
	if err != nil {
		a.serverErr(w, err)
		return
	}
	sp, err := a.store.GetSpot(r.Context(), spotID, u.ID)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sp)
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
