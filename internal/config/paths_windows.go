package config

import (
	"os"
	"path/filepath"
)

func Default() Paths {
	pf := os.Getenv("ProgramFiles")
	if pf == "" {
		pf = `C:\Program Files`
	}
	pd := os.Getenv("ProgramData")
	if pd == "" {
		pd = `C:\ProgramData`
	}
	dir := filepath.Join(pd, "CortenDesk Agent")
	return Paths{
		Binary:    filepath.Join(pf, "CortenDesk Agent", "cortendesk-agent.exe"),
		ConfigDir: dir,
		Config:    filepath.Join(dir, "config.json"),
		State:     filepath.Join(dir, "state.json"),
		CA:        filepath.Join(dir, "ca.pem"),
		Commands:  filepath.Join(dir, "commands.json"),
		Downloads: filepath.Join(dir, "downloads"),
		Log:       filepath.Join(dir, "agent.log"),
	}
}
