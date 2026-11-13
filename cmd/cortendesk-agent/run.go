package main

import (
	"context"
	"flag"
	"fmt"
	"maps"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/marcpope/cortendesk-agent/internal/agent"
	"github.com/marcpope/cortendesk-agent/internal/api"
	"github.com/marcpope/cortendesk-agent/internal/collect"
	"github.com/marcpope/cortendesk-agent/internal/commands"
	"github.com/marcpope/cortendesk-agent/internal/config"
	"github.com/marcpope/cortendesk-agent/internal/logging"
	"github.com/marcpope/cortendesk-agent/internal/service"
	"github.com/marcpope/cortendesk-agent/internal/update"
	"github.com/marcpope/cortendesk-agent/internal/version"
)

func paths(configPath string) config.Paths {
	if configPath != "" {
		return config.ForConfig(configPath)
	}
	return config.Default()
}

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	configPath := fs.String("config", "", "config file (default: the installed one)")
	foreground := fs.Bool("foreground", false, "run without the service manager, log to stderr")
	if err := fs.Parse(args); err != nil {
		return err
	}
	p := paths(*configPath)
	fg := *foreground || service.Interactive()

	logPath := p.Log
	if fg {
		logPath = ""
	}
	log, closer := logging.New(logPath)
	defer closer.Close()

	// Exiting non-zero makes the service manager start the agent again,
	// which is how a new or restored binary takes over.
	restart := func() {
		log.Info("restarting")
		closer.Close()
		os.Exit(update.ExitRestart)
	}
	bin, err := os.Executable()
	if err == nil {
		bin, err = filepath.EvalSymlinks(bin)
	}
	if err != nil {
		log.Error("cannot find own binary", "err", err)
		return err
	}
	store := commands.OpenStore(p.Commands, log)
	guard := &update.Guard{Dir: p.ConfigDir, Binary: bin, Current: version.Version, Store: store, Log: log, Restart: restart}
	if guard.Start() {
		restart()
	}

	cfg, err := config.Load(p.Config)
	if err != nil {
		log.Error("cannot load config", "err", err)
		return err
	}
	updater := &update.Updater{
		Binary: bin, Dir: p.ConfigDir, ConfigPath: p.Config, Current: version.Version,
		Keys: update.Keys(), Store: store, Log: log, Restart: restart,
	}
	handlers := map[string]commands.Spec{api.CmdAgentUpdate: {Run: updater.Run, Timeout: 15 * time.Minute}}
	col := collect.New()
	if !cfg.DisableCommands {
		maps.Copy(handlers, commands.Builtin(&commands.RustDesk{WorkDir: p.Downloads}))
		if spec, ok := handlers[api.CmdRustDeskUpgrade]; ok {
			spec.After = col.ForgetRustDesk
			handlers[api.CmdRustDeskUpgrade] = spec
		}
	}
	runner := &agent.Runner{
		Paths: p, Config: cfg, Log: log, Source: col,
		Commands:  &commands.Runner{Log: log, Store: store, Handlers: handlers},
		OnReached: guard.Confirm,
	}
	if err := runner.Prepare(); err != nil {
		log.Error("bad config", "err", err)
		return err
	}
	run := func(ctx context.Context) {
		go guard.Watch(ctx, update.ConfirmWithin)
		_ = runner.Run(ctx) // only fails in Prepare, already done
	}

	if fg {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		run(ctx)
		return nil
	}
	err = service.Run(p, run)
	if err != nil {
		log.Error("service run failed", "err", err)
	}
	return err
}

// cmdTrial is run by an update on the binary it just downloaded: one
// report with the real config, nothing written. Prints "trial ok <version>"
// on success.
func cmdTrial(args []string) error {
	fs := flag.NewFlagSet("trial", flag.ContinueOnError)
	configPath := fs.String("config", "", "config file (default: the installed one)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	p := paths(*configPath)
	p.State = ""
	cfg, err := config.Load(p.Config)
	if err != nil {
		return err
	}
	log, closer := logging.New("")
	defer closer.Close()
	runner := &agent.Runner{Paths: p, Config: cfg, Log: log}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := runner.Trial(ctx); err != nil {
		return err
	}
	fmt.Println("trial ok", version.Version)
	return nil
}

func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	configPath := fs.String("config", "", "config file (default: the installed one)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	p := paths(*configPath)
	fmt.Printf("Version:      %s\n", version.Version)
	fmt.Printf("Config:       %s\n", p.Config)
	if cfg, err := config.Load(p.Config); err != nil {
		fmt.Printf("              cannot read: %v\n", err)
	} else {
		fmt.Printf("Server:       %s\n", cfg.Server)
		if cfg.Enrolled() {
			fmt.Printf("Agent ID:     %s\n", cfg.AgentID)
		} else {
			fmt.Printf("Agent ID:     not enrolled yet\n")
		}
		if cfg.CAFile != "" {
			fmt.Printf("CA file:      %s\n", cfg.CAFile)
		}
	}
	st := config.LoadState(p.State)
	if st.LastReportAt.IsZero() {
		fmt.Printf("Last report:  never\n")
	} else {
		fmt.Printf("Last report:  %s (%s ago), %s\n", st.LastReportAt.Format(time.RFC3339), ago(st.LastReportAt), st.LastResult)
		if st.LastError != "" {
			fmt.Printf("              %s\n", st.LastError)
		}
		if !st.LastSuccessAt.IsZero() && st.LastResult != "ok" {
			fmt.Printf("Last success: %s\n", st.LastSuccessAt.Format(time.RFC3339))
		}
	}
	if st.Interval > 0 {
		fmt.Printf("Interval:     %ds\n", st.Interval)
	}
	if !st.InventoryAt.IsZero() {
		fmt.Printf("Inventory:    sent %s, hash %.12s\n", st.InventoryAt.Format(time.RFC3339), st.InventoryHash)
	}
	fmt.Printf("Service:      %s\n", service.StatusString(p))
	return nil
}

func ago(t time.Time) time.Duration {
	return time.Since(t).Round(time.Second)
}
