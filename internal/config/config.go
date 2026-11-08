// Package config reads and writes the agent config and state files.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Config is written by install and only read by the service.
type Config struct {
	Server  string `json:"server"`
	AgentID string `json:"agent_id,omitempty"`
	Secret  string `json:"secret,omitempty"`
	// Token lets a pre-written config enroll on first run. It is removed
	// once enrollment succeeds.
	Token  string `json:"token,omitempty"`
	CAFile string `json:"ca_file,omitempty"`
	// DisableCommands refuses every console command except agent.update
	// (install --no-commands).
	DisableCommands bool `json:"disable_commands,omitempty"`
}

func (c *Config) Enrolled() bool { return c.AgentID != "" && c.Secret != "" }

// State is what the service learned at run time. Not secret.
type State struct {
	LastReportAt  time.Time `json:"last_report_at,omitzero"`
	LastSuccessAt time.Time `json:"last_success_at,omitzero"`
	LastResult    string    `json:"last_result,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
	Interval      int       `json:"interval,omitempty"`
	InventoryHash string    `json:"inventory_hash,omitempty"`
	InventoryAt   time.Time `json:"inventory_at,omitzero"`
}

// Paths are the files the agent uses. Default() gives the per-OS install
// layout; ForConfig() puts everything next to a given config file.
type Paths struct {
	Binary    string
	ConfigDir string
	Config    string
	State     string
	CA        string
	// Commands is commands.json: command ids seen, results not yet sent.
	Commands string
	// Downloads holds RustDesk installers while they install.
	Downloads string
	// Log is empty where the service manager collects stdout (Linux).
	Log string
}

func ForConfig(configPath string) Paths {
	p := Default()
	abs, err := filepath.Abs(configPath)
	if err == nil {
		configPath = abs
	}
	dir := filepath.Dir(configPath)
	p.ConfigDir = dir
	p.Config = configPath
	p.State = filepath.Join(dir, "state.json")
	p.CA = filepath.Join(dir, "ca.pem")
	p.Commands = filepath.Join(dir, "commands.json")
	p.Downloads = filepath.Join(dir, "downloads")
	p.Log = ""
	return p
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	c.Server = NormalizeServer(c.Server)
	if c.Server == "" {
		return nil, fmt.Errorf("%s: server is empty", path)
	}
	if c.CAFile != "" && !filepath.IsAbs(c.CAFile) {
		c.CAFile = filepath.Join(filepath.Dir(path), c.CAFile)
	}
	if !c.Enrolled() && c.Token == "" {
		return nil, fmt.Errorf("%s: not enrolled and no token", path)
	}
	return &c, nil
}

func Save(path string, c *Config) error {
	return writeJSON(path, c, 0o600)
}

// LoadState never fails: a missing or broken state file means a fresh start.
func LoadState(path string) State {
	var s State
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s
}

func SaveState(path string, s State) error {
	return writeJSON(path, s, 0o600)
}

// NormalizeServer trims spaces and trailing slashes.
func NormalizeServer(s string) string {
	return strings.TrimRight(strings.TrimSpace(s), "/")
}

// writeJSON writes via a temp file in the same directory so a crash never
// leaves a half-written config.
func writeJSON(path string, v any, mode os.FileMode) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
