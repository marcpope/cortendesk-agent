// Package logging sets up the agent log: a rotating file on Windows and
// macOS, stderr (and so journald) on Linux and when run by hand.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

const (
	MaxSize = 5 << 20
	Keep    = 3
)

// New returns a logger writing to path, or to stderr when path is empty or
// cannot be opened. The returned closer is never nil.
func New(path string) (*slog.Logger, io.Closer) {
	if path == "" {
		return slog.New(slog.NewTextHandler(os.Stderr, nil)), io.NopCloser(nil)
	}
	w, err := NewRotator(path, MaxSize, Keep)
	if err != nil {
		l := slog.New(slog.NewTextHandler(os.Stderr, nil))
		l.Warn("cannot open log file, using stderr", "path", path, "err", err)
		return l, io.NopCloser(nil)
	}
	return slog.New(slog.NewTextHandler(w, nil)), w
}

// Rotator is an io.Writer that renames path to path.1 (path.1 to path.2 and
// so on) once it reaches maxSize, keeping keep old files.
type Rotator struct {
	mu      sync.Mutex
	path    string
	maxSize int64
	keep    int
	f       *os.File
	size    int64
}

func NewRotator(path string, maxSize int64, keep int) (*Rotator, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	r := &Rotator{path: path, maxSize: maxSize, keep: keep}
	if err := r.open(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Rotator) open() error {
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	r.f, r.size = f, st.Size()
	return nil
}

func (r *Rotator) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return 0, os.ErrClosed
	}
	if r.size > 0 && r.size+int64(len(p)) > r.maxSize {
		if err := r.rotate(); err != nil {
			// Keep logging to the old file rather than losing lines.
			fmt.Fprintf(os.Stderr, "log rotate: %v\n", err)
		}
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

// rotate closes the file first: Windows will not rename an open file.
func (r *Rotator) rotate() error {
	if err := r.f.Close(); err != nil {
		return err
	}
	r.f = nil
	os.Remove(fmt.Sprintf("%s.%d", r.path, r.keep))
	for i := r.keep - 1; i >= 1; i-- {
		os.Rename(fmt.Sprintf("%s.%d", r.path, i), fmt.Sprintf("%s.%d", r.path, i+1))
	}
	renameErr := os.Rename(r.path, r.path+".1")
	if err := r.open(); err != nil {
		return err
	}
	return renameErr
}

func (r *Rotator) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}
