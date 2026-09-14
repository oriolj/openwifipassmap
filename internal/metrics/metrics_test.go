package metrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerFailsClosedWithoutToken(t *testing.T) {
	h := Handler("", false, nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("no token, prod: want 401, got %d", rr.Code)
	}
	// dev keeps local runs scrapeable
	rr = httptest.NewRecorder()
	Handler("", true, nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("no token, dev: want 200, got %d", rr.Code)
	}
}

func TestHandlerBearer(t *testing.T) {
	extra := func(context.Context) ([]byte, error) { return []byte("litestream_db_size{db=\"x\"} 1\n"), nil }
	h := Handler("s3cret", false, extra)
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: want 401, got %d", rr.Code)
	}
	req.Header.Set("Authorization", "Bearer s3cret")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("right token: want 200, got %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{"openwifipassmap_app_info{version=", "go_goroutines", "litestream_db_size{db=\"x\"} 1"} {
		if !strings.Contains(body, want) {
			t.Errorf("exposition lacks %q", want)
		}
	}
}

func TestInstrumentRouteLabel(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/spots/{id}", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) })
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := Instrument(mux)
	for _, p := range []string{"/api/spots/abc", "/api/spots/def", "/api/health", "/nope"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, p, nil))
	}
	rr := httptest.NewRecorder()
	Handler("", true, nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rr.Body.String()
	if !strings.Contains(body, `openwifipassmap_http_request_duration_seconds_count{code="404",method="GET",route="GET /api/spots/{id}"} 2`) {
		t.Errorf("pattern label missing/wrong:\n%s", grepLines(body, "duration_seconds_count"))
	}
	if !strings.Contains(body, `route="unmatched"`) {
		t.Errorf("unmatched route not labelled")
	}
	if strings.Contains(body, `/api/health`) {
		t.Errorf("health route must be excluded from the histogram")
	}
}

func grepLines(s, sub string) string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, sub) {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}
