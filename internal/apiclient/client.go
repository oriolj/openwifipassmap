// Package apiclient is a tiny HTTP client for the WiFi Spots backend, used by
// the CLI to bulk-download public spots for an area.
package apiclient

import (
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
