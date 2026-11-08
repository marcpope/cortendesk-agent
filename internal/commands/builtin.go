package commands

import (
	"time"

	"github.com/marcpope/cortendesk-agent/internal/api"
)

// Builtin returns the handlers compiled into this agent. agent.update is
// added by the caller, since it needs the updater.
func Builtin(rd *RustDesk) map[string]Spec {
	return map[string]Spec{
		api.CmdReboot:          {Run: reboot, Timeout: 2 * time.Minute},
		api.CmdOSUpdate:        {Run: osUpdate, Timeout: osUpdateTimeout + 5*time.Minute},
		api.CmdRustDeskUpgrade: {Run: rd.Upgrade, Timeout: time.Hour},
	}
}
