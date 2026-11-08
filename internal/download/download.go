// Package download fetches a file over HTTPS and checks its sha256.
package download

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/marcpope/cortendesk-agent/internal/privnet"
)

// Timeout for a whole download.
const Timeout = 30 * time.Minute

var client = &http.Client{
	Timeout: Timeout,
	Transport: func() *http.Transport {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		// DialContext carries plain http (and proxy connections), so it may
		// only reach a private address, checked once resolved. https gets
		// its own unrestricted dialer.
		private := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second, Control: privnet.Control}
		tr.DialContext = private.DialContext
		dial := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
		tr.DialTLSContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := dial.DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			host, _, _ := net.SplitHostPort(addr)
			cfg := tr.TLSClientConfig.Clone()
			cfg.ServerName = host
			tc := tls.Client(conn, cfg)
			if err := tc.HandshakeContext(ctx); err != nil {
				conn.Close()
				return nil, err
			}
			return tc, nil
		}
		return tr
	}(),
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		return CheckURL(req.URL.String())
	},
}

// CheckURL allows https, and plain http to a private address (the dialer
// checks names once resolved). Files are checked by sha256 either way.
func CheckURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("bad URL %q", raw)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if ip := net.ParseIP(u.Hostname()); ip == nil || privnet.IsPrivate(ip) {
			return nil
		}
	}
	return fmt.Errorf("URL must use https unless it is a private network address: %q", raw)
}

// File downloads rawURL to dst (mode 0600), at most maxBytes, and checks the
// sha256 against want (hex). On any failure dst is removed.
func File(ctx context.Context, rawURL, dst, want string, maxBytes int64) (err error) {
	if err := CheckURL(rawURL); err != nil {
		return err
	}
	want = strings.ToLower(want)
	if len(want) != 64 {
		return errors.New("sha256 must be 64 hex characters")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "cortendesk-agent")
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("download answered %d", res.StatusCode)
	}

	f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			os.Remove(dst)
		}
	}()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(res.Body, maxBytes+1))
	if err != nil {
		return err
	}
	if n > maxBytes {
		return fmt.Errorf("download is larger than %d bytes", maxBytes)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("sha256 mismatch: got %s, want %s", got, want)
	}
	return nil
}
