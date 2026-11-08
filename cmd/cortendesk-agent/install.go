package main

import (
	"context"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/marcpope/cortendesk-agent/internal/agent"
	"github.com/marcpope/cortendesk-agent/internal/client"
	"github.com/marcpope/cortendesk-agent/internal/collect"
	"github.com/marcpope/cortendesk-agent/internal/config"
	"github.com/marcpope/cortendesk-agent/internal/service"
	"github.com/marcpope/cortendesk-agent/internal/update"
)

func cmdInstall(args []string) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	server := fs.String("server", "", "console URL, e.g. https://console.example.com (http only to a private address)")
	token := fs.String("token", "", "enrollment token (cde_...)")
	caFile := fs.String("ca-file", "", "PEM file with a private CA to trust")
	noCommands := fs.Bool("no-commands", false, "refuse console commands except agent updates")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *server == "" || *token == "" {
		return errors.New("--server and --token are required")
	}
	if !service.IsAdmin() {
		return errors.New("install must run as root or an elevated Administrator")
	}
	p := config.Default()
	srv := config.NormalizeServer(*server)

	var caPEM []byte
	if *caFile != "" {
		b, err := os.ReadFile(*caFile)
		if err != nil {
			return err
		}
		if !x509.NewCertPool().AppendCertsFromPEM(b) {
			return fmt.Errorf("no PEM certificates in %s", *caFile)
		}
		caPEM = b
	}

	// Enroll first so a bad token or URL leaves an existing install alone.
	cl, err := client.New(srv, *caFile)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	fmt.Println("Enrolling with", srv)
	resp, err := agent.Enroll(ctx, cl, collect.New(), *token)
	if err != nil {
		if client.StatusOf(err) == 401 {
			return errors.New("the console rejected the token (unknown, revoked, expired or used up)")
		}
		return fmt.Errorf("enroll: %w", err)
	}

	svc, err := service.New(p)
	if err != nil {
		return err
	}
	installed := service.Installed(svc)
	if installed {
		_ = svc.Stop() // so the binary can be replaced
	}

	if err := copySelf(p.Binary); err != nil {
		return fmt.Errorf("copy binary: %w", err)
	}
	if err := config.SecureDir(p.ConfigDir); err != nil {
		return err
	}
	cfg := &config.Config{Server: srv, AgentID: resp.AgentID, Secret: resp.Secret, DisableCommands: *noCommands}
	if caPEM != nil {
		if err := os.WriteFile(p.CA, caPEM, 0o600); err != nil {
			return err
		}
		cfg.CAFile = p.CA
	} else {
		os.Remove(p.CA)
	}
	if err := config.Save(p.Config, cfg); err != nil {
		return err
	}
	// New identity: forget the old inventory hash and last result.
	os.Remove(p.State)

	if !installed {
		if err := svc.Install(); err != nil {
			return fmt.Errorf("register service: %w", err)
		}
	}
	if err := svc.Start(); err != nil {
		return fmt.Errorf("start service: %w", err)
	}
	fmt.Printf("Installed. Agent ID %s, service %s.\n", resp.AgentID, service.StatusString(p))
	return nil
}

// copySelf puts the running executable at dst, unless it already is dst.
func copySelf(dst string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if s, err := filepath.EvalSymlinks(self); err == nil {
		self = s
	}
	if same(self, dst) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(self)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func same(a, b string) bool {
	sa, err1 := os.Stat(a)
	sb, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(sa, sb)
}

func cmdUninstall(args []string) error {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !service.IsAdmin() {
		return errors.New("uninstall must run as root or an elevated Administrator")
	}
	p := config.Default()
	svc, err := service.New(p)
	if err != nil {
		return err
	}
	if service.Installed(svc) {
		_ = svc.Stop()
		if err := svc.Uninstall(); err != nil {
			return fmt.Errorf("remove service: %w", err)
		}
		fmt.Println("Service removed.")
	}
	var errs []error
	if err := os.RemoveAll(p.ConfigDir); err != nil {
		errs = append(errs, err)
	}
	if p.Log != "" && filepath.Dir(p.Log) != p.ConfigDir {
		if err := os.RemoveAll(filepath.Dir(p.Log)); err != nil {
			errs = append(errs, err)
		}
	}
	update.Cleanup(p.Binary)
	if err := service.RemoveFile(p.Binary); err != nil {
		errs = append(errs, err)
	}
	// Only remove the parent when it is our own folder, never /usr/local/bin.
	if dir := filepath.Dir(p.Binary); filepath.Base(dir) == "CortenDesk Agent" {
		_ = service.RemoveFile(dir)
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	fmt.Println("CortenDesk Agent removed.")
	return nil
}
