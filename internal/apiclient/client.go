// Package apiclient is a tiny HTTP client for the WiFi Spots backend, used by
// the CLI to bulk-download public spots for an area.
package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/oriolj/wifi_psw_sharer/internal/models"
)

// Client talks to a WiFi Spots server.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

// New returns a Client for baseURL.
func New(baseURL string) *Client {
	return &Client{BaseURL: baseURL, HTTP: &http.Client{Timeout: 30 * time.Second}}
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
	u := fmt.Sprintf("%s/api/spots/area?%s", c.BaseURL, q.Encode())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("server returned %s", resp.Status)
	}
	var out areaResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
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
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.BaseURL+"/api/auth/login", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("login failed: %s", resp.Status)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.Token, nil
}

// CreateSpot POSTs a spot with the given bearer token and returns its id.
func (c *Client) CreateSpot(ctx context.Context, token string, in SpotInput) (string, error) {
	body, _ := json.Marshal(in)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.BaseURL+"/api/spots", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		msg := struct {
			Error string `json:"error"`
		}{}
		_ = json.NewDecoder(resp.Body).Decode(&msg)
		if msg.Error != "" {
			return "", fmt.Errorf("%s", msg.Error)
		}
		return "", fmt.Errorf("create spot failed: %s", resp.Status)
	}
	var out models.Spot
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.ID, nil
}
