package execx

import (
	"os/exec"
	"syscall"
)

// hide stops a console window flashing up when the agent runs interactively.
func hide(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}
