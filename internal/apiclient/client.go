// Package apiclient is a tiny HTTP client for the OpenWifiPassMap backend, used by
// the CLI to bulk-download public spots and to upload spots from a CSV.
package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/oriolj/openwifipassmap/internal/models"
)

// Client talks to an OpenWifiPassMap server.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

// New returns a Client for baseURL.
func New(baseURL string) *Client {
	return &Client{BaseURL: baseURL, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// doJSON performs an HTTP request with an optional JSON body and bearer token,
// checks the status, and decodes the JSON response into out (if non-nil). On a
// non-wantStatus response it prefers the server's {"error": ...} message.
func (c *Client) doJSON(ctx context.Context, method, path, token string, body, out any, wantStatus int) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		if e.Error != "" {
			return fmt.Errorf("%s", e.Error)
		}
		return fmt.Errorf("%s %s: %s", method, path, resp.Status)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

type areaResponse struct {
	Results    []*models.Spot `json:"results"`
	NextCursor string         `json:"next_cursor"`
}

// AreaPage fetches one page of spots within radiusKM of (lat,lng). cursor is ""
// for the first page; the returned next cursor is "" when there are no more.
func (c *Client) AreaPage(ctx context.Context, lat, lng, radiusKM float64, cursor string) ([]*models.Spot, string, error) {
	q := url.Values{}
	q.Set("lat", strconv.FormatFloat(lat, 'f', -1, 64))
	q.Set("lng", strconv.FormatFloat(lng, 'f', -1, 64))
	q.Set("radius_km", strconv.FormatFloat(radiusKM, 'f', -1, 64))
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	var out areaResponse
	if err := c.doJSON(ctx, http.MethodGet, "/api/spots/area?"+q.Encode(), "", nil, &out, http.StatusOK); err != nil {
		return nil, "", err
	}
	return out.Results, out.NextCursor, nil
}

// SpotInput is the payload for creating a spot via the API. lat/lng have no
// omitempty so a legitimate 0 coordinate is still sent (the server requires both).
type SpotInput struct {
	VenueName string  `json:"venue_name,omitempty"`
	ESSID     string  `json:"essid"`
	Password  string  `json:"password,omitempty"`
	AuthType  string  `json:"auth_type,omitempty"`
	Lat       float64 `json:"lat"`
	Lng       float64 `json:"lng"`
	Notes     string  `json:"notes,omitempty"`
}

// Login authenticates and returns a bearer token.
func (c *Client) Login(ctx context.Context, username, password string) (string, error) {
	var out struct {
		Token string `json:"token"`
	}
	err := c.doJSON(ctx, http.MethodPost, "/api/auth/login", "",
		map[string]string{"username": username, "password": password}, &out, http.StatusOK)
	if err != nil {
		return "", err
	}
	return out.Token, nil
}

// CreateSpot POSTs a spot with the given bearer token and returns its id.
func (c *Client) CreateSpot(ctx context.Context, token string, in SpotInput) (string, error) {
	var out models.Spot
	if err := c.doJSON(ctx, http.MethodPost, "/api/spots", token, in, &out, http.StatusCreated); err != nil {
		return "", err
	}
	return out.ID, nil
}
