package client

import (
	"net"
	"testing"

	"github.com/marcpope/cortendesk-agent/internal/privnet"
)

func TestIsPrivate(t *testing.T) {
	for ip, want := range map[string]bool{
		"127.0.0.1":     true,
		"::1":           true,
		"10.20.30.40":   true,
		"172.16.0.1":    true,
		"172.31.255.1":  true,
		"192.168.1.20":  true,
		"100.64.0.1":    true,
		"100.127.9.9":   true,
		"fd12:3456::1":  true,
		"172.32.0.1":    false,
		"100.128.0.1":   false,
		"8.8.8.8":       false,
		"203.0.113.5":   false,
		"2001:db8::1":   false,
		"169.254.10.10": false,
	} {
		if got := privnet.IsPrivate(net.ParseIP(ip)); got != want {
			t.Errorf("IsPrivate(%s) = %v, want %v", ip, got, want)
		}
	}
}

func TestNewPlainHTTP(t *testing.T) {
	for server, ok := range map[string]bool{
		"https://console.example.com": true,
		"http://192.168.1.20:8000":    true,
		"http://[fd00::5]:8000":       true,
		"http://localhost:8090":       true,
		"http://console.lan":          true, // checked when it connects
		"http://8.8.8.8":              false,
		"ftp://192.168.1.20":          false,
	} {
		_, err := New(server, "")
		if (err == nil) != ok {
			t.Errorf("New(%q) error = %v, want ok=%v", server, err, ok)
		}
	}
}

func TestPrivateOnly(t *testing.T) {
	if err := privnet.Control("tcp", "10.0.0.5:80", nil); err != nil {
		t.Errorf("private address refused: %v", err)
	}
	if err := privnet.Control("tcp", "[fd00::1]:80", nil); err != nil {
		t.Errorf("private v6 address refused: %v", err)
	}
	if err := privnet.Control("tcp", "93.184.216.34:80", nil); err == nil {
		t.Error("public address allowed over plain http")
	}
}
