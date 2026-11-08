// Package service registers the agent with the OS service manager
// (Windows SCM, launchd, systemd) through kardianos/service.
package service

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"time"

	ks "github.com/kardianos/service"
	"github.com/marcpope/cortendesk-agent/internal/config"
)

const (
	DisplayName = "CortenDesk Agent"
	Description = "Reports machine health and inventory to the CortenDesk console."
)

var ErrNotInstalled = ks.ErrNotInstalled

func newConfig(p config.Paths) *ks.Config {
	opts := ks.KeyValue{
		// launchd
		"RunAtLoad": true,
		"KeepAlive": true,
		// Windows
		"StartType":              "automatic",
		"OnFailure":              "restart",
		"OnFailureDelayDuration": "30s",
		// systemd
		"Restart": "always",
	}
	if p.Log != "" {
		// launchd sends stray stdout/stderr (a panic, say) next to our log.
		opts["LogDirectory"] = filepath.Dir(p.Log)
	}
	return &ks.Config{
		Name:         Name,
		DisplayName:  DisplayName,
		Description:  Description,
		Executable:   p.Binary,
		Arguments:    []string{"run"},
		Dependencies: dependencies,
		Option:       opts,
	}
}

// New returns a handle for controlling the installed service.
func New(p config.Paths) (ks.Service, error) {
	return ks.New(&program{}, newConfig(p))
}

// Interactive is false when the process was started by the service manager.
func Interactive() bool { return ks.Interactive() }

// StatusString describes the service state for `status`.
func StatusString(p config.Paths) string {
	s, err := New(p)
	if err != nil {
		return "unknown (" + err.Error() + ")"
	}
	st, err := s.Status()
	switch {
	case errors.Is(err, ks.ErrNotInstalled):
		return "not installed"
	case err != nil:
		return "unknown (" + err.Error() + ")"
	case st == ks.StatusRunning:
		return "running"
	case st == ks.StatusStopped:
		return "stopped"
	}
	return "unknown"
}

// Installed reports whether the service is registered.
func Installed(s ks.Service) bool {
	_, err := s.Status()
	return !errors.Is(err, ks.ErrNotInstalled)
}

// Run hands control to the service manager and runs fn until it says stop.
func Run(p config.Paths, fn func(ctx context.Context)) error {
	prog := &program{fn: fn}
	s, err := ks.New(prog, newConfig(p))
	if err != nil {
		return err
	}
	return s.Run()
}

type program struct {
	fn     func(ctx context.Context)
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func (p *program) Start(ks.Service) error {
	if p.fn == nil {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.fn(ctx)
	}()
	return nil
}

// Stop waits a few seconds for an in-flight report to finish.
func (p *program) Stop(ks.Service) error {
	if p.cancel == nil {
		return nil
	}
	p.cancel()
	done := make(chan struct{})
	go func() { p.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
	return nil
}
