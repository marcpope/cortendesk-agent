// Package health runs the slow, read-only checks (antivirus, firewall,
// encryption, updates, SMART, watched services, event log errors, Wi-Fi)
// in the background and keeps the latest results for the report.
package health

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/marcpope/cortendesk-agent/internal/api"
)

// RefreshEvery is how often the full set of checks runs.
const RefreshEvery = 30 * time.Minute

// Check fills one key of out. An error, or leaving the key empty, means the
// key is left out of the report.
type Check struct {
	Key     string
	Timeout time.Duration
	Run     func(ctx context.Context, watch []string, out *api.Health) error
}

// Manager owns the cached health and the background refresh.
type Manager struct {
	Log    *slog.Logger
	Checks []Check
	Now    func() time.Time

	mu      sync.Mutex
	h       api.Health
	have    bool
	watch   []string
	running map[string]bool
	kick    chan struct{}
}

// New returns a manager with this OS's checks.
func New(log *slog.Logger) *Manager {
	return &Manager{Log: log, Checks: checks}
}

func (m *Manager) init() {
	if m.running == nil {
		m.running = map[string]bool{}
		m.kick = make(chan struct{}, 1)
	}
	if m.Now == nil {
		m.Now = time.Now
	}
	if m.Log == nil {
		m.Log = slog.Default()
	}
}

// Run refreshes now, then every RefreshEvery, and the services check
// whenever the watch list changes. It returns when ctx ends.
func (m *Manager) Run(ctx context.Context) {
	m.mu.Lock()
	m.init()
	kick := m.kick
	m.mu.Unlock()

	m.Refresh(ctx, "")
	t := time.NewTicker(RefreshEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.Refresh(ctx, "")
		case <-kick:
			m.Refresh(ctx, "services")
		}
	}
}

// Refresh starts the checks (all of them, or only the one with key only)
// that are not already running, each in its own goroutine with its own
// timeout. It returns a channel closed when the started checks finish.
func (m *Manager) Refresh(ctx context.Context, only string) <-chan struct{} {
	m.mu.Lock()
	m.init()
	var wg sync.WaitGroup
	for _, c := range m.Checks {
		if only != "" && c.Key != only {
			continue
		}
		// Services is quick and must follow watch list changes, so a new
		// run may overlap an old one; stale results are dropped on merge.
		if c.Key != "services" && m.running[c.Key] {
			continue
		}
		m.running[c.Key] = true
		watch := slices.Clone(m.watch)
		wg.Add(1)
		go func(c Check) {
			defer wg.Done()
			m.runOne(ctx, c, watch)
		}(c)
	}
	m.mu.Unlock()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	return done
}

func (m *Manager) runOne(ctx context.Context, c Check, watch []string) {
	cctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	var out api.Health
	err := func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				m.Log.Error("health check panicked", "check", c.Key, "panic", r)
				out = api.Health{}
				err = nil
			}
		}()
		return c.Run(cctx, watch, &out)
	}()
	if err != nil {
		m.Log.Debug("health check failed", "check", c.Key, "err", err)
		out = api.Health{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if c.Key != "services" {
		m.running[c.Key] = false
	} else if !slices.Equal(watch, m.watch) {
		return // a newer run has the current list
	}
	if ctx.Err() != nil {
		return
	}
	copyKey(c.Key, &m.h, &out)
	m.h.CollectedAt = m.Now().Unix()
	m.have = true
}

// Get returns a copy of the cached health, or nil before the first check
// has finished.
func (m *Manager) Get() *api.Health {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.have {
		return nil
	}
	h := m.h
	return &h
}

// SetWatch takes the console's watch_services list. Unsafe names are
// dropped. A change triggers a services check right away.
func (m *Manager) SetWatch(names []string) bool {
	if m == nil {
		return false
	}
	clean := SanitizeServices(names)
	m.mu.Lock()
	m.init()
	if slices.Equal(clean, m.watch) {
		m.mu.Unlock()
		return false
	}
	m.watch = clean
	if len(clean) == 0 {
		m.h.Services = nil
	}
	m.mu.Unlock()
	select {
	case m.kick <- struct{}{}:
	default:
	}
	return true
}

// Watch returns the current sanitized watch list.
func (m *Manager) Watch() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.watch)
}

func copyKey(key string, dst, src *api.Health) {
	switch key {
	case "antivirus":
		dst.Antivirus = src.Antivirus
	case "firewall":
		dst.Firewall = src.Firewall
	case "encryption":
		dst.Encryption = src.Encryption
	case "updates":
		dst.Updates = src.Updates
	case "smart":
		dst.Smart = src.Smart
	case "services":
		dst.Services = src.Services
	case "event_errors_24h":
		dst.EventErrors24h = src.EventErrors24h
	case "wifi_ssid":
		dst.WifiSSID = src.WifiSSID
	}
}

const maxServiceName = 128

// SafeServiceName allows letters, digits, spaces and $ . - _ @ ( ), up to
// 128 characters, and no leading "-" so a name can never read as a flag.
// Names come from the console and are only ever passed as single argv
// elements or API arguments, never through a shell.
func SafeServiceName(s string) bool {
	if s == "" || len(s) > maxServiceName || strings.TrimSpace(s) == "" || s[0] == '-' {
		return false
	}
	for _, r := range s {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
		case strings.ContainsRune(" $.-_@()", r):
		default:
			return false
		}
	}
	return true
}

// SanitizeServices drops unsafe names, duplicates and empty entries, and
// sorts what is left.
func SanitizeServices(in []string) []string {
	var out []string
	for _, s := range in {
		if SafeServiceName(s) && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	slices.Sort(out)
	return out
}

func ptr[T any](v T) *T { return &v }
