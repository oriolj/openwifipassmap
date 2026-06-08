// Package email sends transactional email — currently just password-reset
// magic links — through Resend's HTTP API. When no API key is configured (local
// dev), it falls back to logging the message so flows still work end-to-end
// without sending real mail. Callers should send off the request hot path
// (e.g. in a goroutine); Send itself is a single blocking HTTP round-trip.
package email

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// defaultFrom is used when RESEND_FROM is unset.
const defaultFrom = "no-reply@oriolj.com"

const resendEndpoint = "https://api.resend.com/emails"

// Sender delivers a single email. Implementations must be safe for concurrent use.
type Sender interface {
	Send(ctx context.Context, to, subject, htmlBody, textBody string) error
}

// New returns a Resend-backed Sender, or a logging fallback when apiKey is
// empty. from defaults to defaultFrom when blank.
func New(apiKey, from string, log *slog.Logger) Sender {
	if log == nil {
		log = slog.Default()
	}
	if from == "" {
		from = defaultFrom
	}
	if apiKey == "" {
		log.Warn("email: RESEND_API_KEY unset — emails will be logged, not delivered")
		return &logSender{log: log}
	}
	return &resendSender{
		apiKey: apiKey,
		from:   from,
		client: &http.Client{Timeout: 10 * time.Second},
		log:    log,
	}
}

// logSender is the dev fallback: it records what would have been sent.
type logSender struct{ log *slog.Logger }

func (l *logSender) Send(_ context.Context, to, subject, _ /*html*/, textBody string) error {
	l.log.Info("email (not delivered: no RESEND_API_KEY)",
		"to", to, "subject", subject, "text", textBody)
	return nil
}

type resendSender struct {
	apiKey string
	from   string
	client *http.Client
	log    *slog.Logger
}

func (r *resendSender) Send(ctx context.Context, to, subject, htmlBody, textBody string) error {
	payload, err := json.Marshal(map[string]any{
		"from":    r.from,
		"to":      []string{to},
		"subject": subject,
		"html":    htmlBody,
		"text":    textBody,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, resendEndpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("resend: status %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}
	return nil
}
