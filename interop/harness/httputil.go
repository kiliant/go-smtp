package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// GetJSON issues a GET to url and decodes the JSON response body into out.
// It exists so each HTTP-backed sink (Mailpit, GreenMail) does not
// reimplement request construction, context wiring and error formatting —
// their response schemas differ, so the decoding itself stays in each
// server's own sink file.
func GetJSON(ctx context.Context, url string, out any) error {
	body, err := getBody(ctx, url)
	if err != nil {
		return err
	}
	defer func() { _ = body.Close() }()
	if err := json.NewDecoder(body).Decode(out); err != nil {
		return fmt.Errorf("harness: decoding JSON from %s: %w", url, err)
	}
	return nil
}

// GetBytes issues a GET to url and returns the raw response body.
func GetBytes(ctx context.Context, url string) ([]byte, error) {
	body, err := getBody(ctx, url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = body.Close() }()
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("harness: reading body from %s: %w", url, err)
	}
	return data, nil
}

func getBody(ctx context.Context, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("harness: building request for %s: %w", url, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("harness: GET %s: %w", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("harness: GET %s: unexpected status %s", url, resp.Status)
	}
	return resp.Body, nil
}

// DeleteURL issues a DELETE to url and discards the response body.
func DeleteURL(ctx context.Context, url string) error {
	return doDelete(ctx, url, nil)
}

// DeleteJSON issues a DELETE to url with body JSON-encoded as the request
// body, for an API (e.g. Mailpit) whose bulk-delete endpoint scopes to a
// specific set of IDs only when given one, and deletes everything otherwise.
func DeleteJSON(ctx context.Context, url string, body any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("harness: encoding DELETE body for %s: %w", url, err)
	}
	return doDelete(ctx, url, bytes.NewReader(encoded))
}

func doDelete(ctx context.Context, url string, body io.Reader) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, body)
	if err != nil {
		return fmt.Errorf("harness: building DELETE for %s: %w", url, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("harness: DELETE %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("harness: DELETE %s: unexpected status %s", url, resp.Status)
	}
	return nil
}
