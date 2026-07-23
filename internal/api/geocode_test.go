package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeNominatim returns a canned jsonv2 response and records requests.
func fakeNominatim(t *testing.T, items []nominatimItem) (*httptest.Server, *int32, *url.Values) {
	t.Helper()
	var hits int32
	var lastQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		lastQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(items)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits, &lastQuery
}

func newTestAPI(base string) *API {
	g := newGeocoder(base)
	g.minGap = time.Millisecond
	return &API{log: slog.Default(), geo: g}
}

func TestGeocodeHappyPath(t *testing.T) {
	srv, _, _ := fakeNominatim(t, []nominatimItem{
		{Name: "Girona", DisplayName: "Girona, Gironès, Catalonia, Spain", Lat: "41.98", Lon: "2.82",
			BoundingBox: []string{"41.94", "42.02", "2.77", "2.86"}},
		{Name: "Café Foo", DisplayName: "Café Foo, Girona, Spain", Lat: "41.9812", Lon: "2.8214"},
	})
	a := newTestAPI(srv.URL)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/geocode?q=girona", nil)
	a.geocode(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var out struct {
		Results []geocodeResult `json:"results"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(out.Results))
	}
	r0 := out.Results[0]
	if r0.Name != "Girona" || r0.Lat != 41.98 || r0.Lng != 2.82 {
		t.Errorf("result[0] = %+v, want parsed Girona coords", r0)
	}
	if len(r0.Bbox) != 4 || r0.Bbox[0] != 41.94 || r0.Bbox[3] != 2.86 {
		t.Errorf("bbox = %v, want [41.94 42.02 2.77 2.86]", r0.Bbox)
	}
	r1 := out.Results[1]
	if r1.Bbox != nil {
		t.Errorf("result[1].Bbox = %v, want nil (no boundingbox in fixture)", r1.Bbox)
	}
}

func TestGeocodeForwardsQueryAndUserAgent(t *testing.T) {
	srv, _, lastQuery := fakeNominatim(t, nil)
	a := newTestAPI(srv.URL)

	req := httptest.NewRequest(http.MethodGet, "/api/geocode?q=restaurant+el+celler", nil)
	req.Header.Set("Accept-Language", "ca-ES,ca;q=0.9")
	a.geocode(httptest.NewRecorder(), req)

	q := *lastQuery
	if q.Get("q") != "restaurant el celler" {
		t.Errorf("q forwarded = %q, want %q", q.Get("q"), "restaurant el celler")
	}
	if q.Get("format") != "jsonv2" {
		t.Errorf("format = %q, want jsonv2", q.Get("format"))
	}
	if q.Get("limit") != "5" {
		t.Errorf("limit = %q, want 5", q.Get("limit"))
	}
	if q.Get("accept-language") != "ca-ES" {
		t.Errorf("accept-language = %q, want ca-ES", q.Get("accept-language"))
	}
}

func TestGeocodeCachesRepeatedQuery(t *testing.T) {
	srv, hits, _ := fakeNominatim(t, []nominatimItem{{Name: "Girona", Lat: "41.98", Lon: "2.82"}})
	a := newTestAPI(srv.URL)

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/geocode?q=girona", nil)
		a.geocode(httptest.NewRecorder(), req)
	}
	if got := atomic.LoadInt32(hits); got != 1 {
		t.Errorf("upstream hits = %d, want 1 (second call should be cached)", got)
	}
}

func TestGeocodeThrottlesDistinctQueries(t *testing.T) {
	srv, _, _ := fakeNominatim(t, []nominatimItem{{Name: "X", Lat: "1", Lon: "1"}})
	a := newTestAPI(srv.URL)
	a.geo.minGap = 60 * time.Millisecond

	start := time.Now()
	req1 := httptest.NewRequest(http.MethodGet, "/api/geocode?q=one", nil)
	a.geocode(httptest.NewRecorder(), req1)
	req2 := httptest.NewRequest(http.MethodGet, "/api/geocode?q=two", nil)
	a.geocode(httptest.NewRecorder(), req2)
	elapsed := time.Since(start)

	if elapsed < 60*time.Millisecond {
		t.Errorf("elapsed = %s, want >= 60ms (throttle should space distinct queries)", elapsed)
	}
}

func TestGeocodeEmptyQuery(t *testing.T) {
	a := newTestAPI("http://unused.invalid")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/geocode", nil)
	a.geocode(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestGeocodeTooLongQuery(t *testing.T) {
	a := newTestAPI("http://unused.invalid")
	rec := httptest.NewRecorder()
	long := make([]byte, 201)
	for i := range long {
		long[i] = 'a'
	}
	req := httptest.NewRequest(http.MethodGet, "/api/geocode?q="+string(long), nil)
	a.geocode(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestGeocodeUpstreamErrorMapsTo502(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	a := newTestAPI(srv.URL)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/geocode?q=girona", nil)
	a.geocode(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}

func TestGeocodeEmptyResultsIsEmptyArrayNotNull(t *testing.T) {
	srv, _, _ := fakeNominatim(t, []nominatimItem{})
	a := newTestAPI(srv.URL)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/geocode?q=nowhereatall", nil)
	a.geocode(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"results":[]`) {
		t.Errorf("body = %s, want results to be [] not null", body)
	}
}

func TestGeocodeBusyReturns503(t *testing.T) {
	srv, _, _ := fakeNominatim(t, []nominatimItem{{Name: "X", Lat: "1", Lon: "1"}})
	a := newTestAPI(srv.URL)
	// Fill the throttle queue so the next search sees it full.
	a.geo.queue <- struct{}{}
	a.geo.queue <- struct{}{}
	a.geo.queue <- struct{}{}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/geocode?q=girona", nil)
	a.geocode(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}
