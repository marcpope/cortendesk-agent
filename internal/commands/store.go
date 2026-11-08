package commands

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/marcpope/cortendesk-agent/internal/api"
)

// seenMax is how many command ids are remembered for de-duplication.
const seenMax = 200

// badVersionFor is how long an agent version that failed to update stays
// refused.
const badVersionFor = 24 * time.Hour

// Pending is a result the console has not accepted yet.
type Pending struct {
	ID     string            `json:"id"`
	Result api.CommandResult `json:"result"`
}

type storeData struct {
	Seen    []string             `json:"seen,omitempty"`
	Pending []Pending            `json:"pending,omitempty"`
	Bad     map[string]time.Time `json:"bad_versions,omitempty"`
}

// Store is commands.json next to the config: ids already run, results not
// yet delivered, and agent versions that failed to update. Safe for
// concurrent use.
type Store struct {
	path string
	log  *slog.Logger
	mu   sync.Mutex
	d    storeData
	// Now is the clock; tests replace it.
	Now func() time.Time
}

// OpenStore loads path. A missing or broken file starts empty. An empty
// path keeps everything in memory.
func OpenStore(path string, log *slog.Logger) *Store {
	s := &Store{path: path, log: log, Now: time.Now}
	if path != "" {
		if b, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(b, &s.d)
		}
	}
	return s
}

// MarkSeen records id and reports whether it is new.
func (s *Store) MarkSeen(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if slices.Contains(s.d.Seen, id) {
		return false
	}
	s.d.Seen = append(s.d.Seen, id)
	if over := len(s.d.Seen) - seenMax; over > 0 {
		s.d.Seen = slices.Clone(s.d.Seen[over:])
	}
	s.save()
	return true
}

func (s *Store) AddPending(id string, r api.CommandResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.d.Pending = slices.DeleteFunc(s.d.Pending, func(p Pending) bool { return p.ID == id })
	s.d.Pending = append(s.d.Pending, Pending{ID: id, Result: r})
	s.save()
}

func (s *Store) Pending() []Pending {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.d.Pending)
}

func (s *Store) RemovePending(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.d.Pending = slices.DeleteFunc(s.d.Pending, func(p Pending) bool { return p.ID == id })
	s.save()
}

// MarkBad refuses version for a day.
func (s *Store) MarkBad(version string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.d.Bad == nil {
		s.d.Bad = map[string]time.Time{}
	}
	s.d.Bad[version] = s.Now()
	s.save()
}

// IsBad reports whether version failed to update within the last day.
func (s *Store) IsBad(version string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	at, ok := s.d.Bad[version]
	return ok && s.Now().Sub(at) < badVersionFor
}

func (s *Store) save() {
	if s.path == "" {
		return
	}
	b, err := json.MarshalIndent(s.d, "", "  ")
	if err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".tmp-*")
	if err != nil {
		s.log.Warn("could not save commands.json", "err", err)
		return
	}
	name := tmp.Name()
	_ = tmp.Chmod(0o600)
	_, werr := tmp.Write(append(b, '\n'))
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		os.Remove(name)
		s.log.Warn("could not save commands.json", "err", werr)
		return
	}
	if err := os.Rename(name, s.path); err != nil {
		os.Remove(name)
		s.log.Warn("could not save commands.json", "err", err)
	}
}
