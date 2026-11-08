package download

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckURL(t *testing.T) {
	for raw, ok := range map[string]bool{
		"https://github.com/x":       true,
		"http://127.0.0.1:8090/a":    true,
		"http://192.168.1.20:8090/a": true,
		"http://console.lan/a":       true, // checked by the dialer
		"http://8.8.8.8/a":           false,
		"ftp://10.0.0.1/a":           false,
	} {
		if err := CheckURL(raw); (err == nil) != ok {
			t.Errorf("CheckURL(%q) = %v, want ok=%v", raw, err, ok)
		}
	}
}

func TestFileOverPlainHTTPToLoopback(t *testing.T) {
	body := []byte("agent")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write(body) }))
	defer srv.Close()
	sum := sha256.Sum256(body)
	dst := filepath.Join(t.TempDir(), "f")
	if err := File(context.Background(), srv.URL+"/f", dst, hex.EncodeToString(sum[:]), 100); err != nil {
		t.Fatal(err)
	}
}

func TestPlainHTTPToPublicNameRefused(t *testing.T) {
	err := File(context.Background(), "http://example.com/x", filepath.Join(t.TempDir(), "f"), strings.Repeat("0", 64), 100)
	if err == nil || !strings.Contains(err.Error(), "private network") {
		t.Fatalf("want a private-network refusal, got %v", err)
	}
}
