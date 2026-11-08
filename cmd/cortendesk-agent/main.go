// Command cortendesk-agent reports machine health and inventory to a
// CortenDesk console.
package main

import (
	"fmt"
	"os"
	"runtime"

	"github.com/marcpope/cortendesk-agent/internal/version"
)

const usage = `Usage: cortendesk-agent <command> [flags]

Commands:
  install --server URL --token T [--ca-file F] [--no-commands]
                 copy the agent into place, enroll, start the service;
                 --no-commands refuses console commands (except updates
                 of the agent itself)
  uninstall      stop and remove the service, config and binary
  run [--config F] [--foreground]
                 the service entry point; --config runs against another
                 config file, --foreground logs to stderr
  status [--config F]
                 show config, last report and service state
  version        print the version
  trial [--config F]
                 send one report and exit (used by self-update)
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "install":
		err = cmdInstall(args)
	case "uninstall":
		err = cmdUninstall(args)
	case "run":
		err = cmdRun(args)
	case "status":
		err = cmdStatus(args)
	case "trial":
		err = cmdTrial(args)
	case "version", "--version", "-v":
		fmt.Printf("cortendesk-agent %s %s/%s\n", version.Version, runtime.GOOS, runtime.GOARCH)
	case "help", "--help", "-h":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
