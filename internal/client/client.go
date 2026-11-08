// Package client talks to the console's /agent/v1 API.
package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/marcpope/cortendesk-agent/internal/api"
	"github.com/marcpope/cortendesk-agent/internal/privnet"
	"github.com/marcpope/cortendesk-agent/internal/version"
)

const (
	requestTimeout = 30 * time.Second
	maxBody        = 1 << 20
)

// APIError is a non-2xx answer from the console.
type APIError struct {
	Status int
	Code   string
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("console answered %d %s", e.Status, e.Code)
	}
	return fmt.Sprintf("console answered %d", e.Status)
}

// StatusOf returns the HTTP status behind err, or 0 for network errors.
func StatusOf(err error) int {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Status
	}
	return 0
}

type Client struct {
	base string
	http *http.Client

	// Credentials, set after enrollment.
	AgentID string
	Secret  string
}

// New builds a client for server. The system trust store is used, plus the
// PEM certificates in caFile when given. There is deliberately no way to
// skip verification. Plain http is accepted only to a private network
// address (see privnet), checked on every connection, so a name that
// resolves to a public address is refused too.
func New(server, caFile string) (*Client, error) {
	server = strings.TrimRight(strings.TrimSpace(server), "/")
	u, err := url.Parse(server)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("invalid server URL %q", server)
	}
	plain := false
	switch u.Scheme {
	case "https":
	case "http":
		if ip := net.ParseIP(u.Hostname()); ip != nil && !privnet.IsPrivate(ip) {
			return nil, errors.New("server URL must use https unless it is a private network address")
		}
		plain = true
	default:
		return nil, fmt.Errorf("invalid server URL %q", server)
	}

	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("read CA file: %w", err)
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("no PEM certificates in %s", caFile)
		}
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	if plain {
		// No proxy: the check below must see the console's own address.
		tr.Proxy = nil
		d := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second, Control: privnet.Control}
		tr.DialContext = d.DialContext
	}
	return &Client{
		base: server,
		http: &http.Client{Transport: tr, Timeout: requestTimeout},
	}, nil
}

func (c *Client) Enroll(ctx context.Context, req api.EnrollRequest) (api.EnrollResponse, error) {
	var resp api.EnrollResponse
	err := c.post(ctx, "/agent/v1/enroll", false, req, &resp)
	if err == nil && (resp.AgentID == "" || resp.Secret == "") {
		err = errors.New("enroll response is missing agent_id or secret")
	}
	return resp, err
}

// Report posts a report. On 403 the response is still decoded, since the
// console sends the interval to wait.
func (c *Client) Report(ctx context.Context, r api.Report) (api.ReportResponse, error) {
	var resp api.ReportResponse
	err := c.post(ctx, "/agent/v1/report", true, r, &resp)
	return resp, err
}

func (c *Client) Inventory(ctx context.Context, inv api.Inventory) error {
	var resp map[string]any
	return c.post(ctx, "/agent/v1/inventory", true, inv, &resp)
}

// CommandResult posts the status of a command. id comes from the console,
// so it is checked before it goes into the path.
func (c *Client) CommandResult(ctx context.Context, id string, r api.CommandResult) error {
	if !commandIDRe.MatchString(id) {
		return fmt.Errorf("bad command id %q", id)
	}
	var resp map[string]any
	return c.post(ctx, "/agent/v1/commands/"+id, true, r, &resp)
}

var commandIDRe = regexp.MustCompile(`^[0-9A-Za-z]{1,40}$`)

func (c *Client) post(ctx context.Context, path string, auth bool, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "cortendesk-agent/"+version.Version)
	if auth {
		req.Header.Set("Authorization", "Bearer "+c.AgentID+"."+c.Secret)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, maxBody))
	if err != nil {
		return err
	}
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		if len(bytes.TrimSpace(data)) == 0 {
			return nil
		}
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("bad JSON from console: %w", err)
		}
		return nil
	}
	ae := &APIError{Status: res.StatusCode}
	var e api.Error
	if json.Unmarshal(data, &e) == nil {
		ae.Code = e.Error
	}
	// Error bodies can carry useful fields (403 sends interval).
	_ = json.Unmarshal(data, out)
	return ae
}
