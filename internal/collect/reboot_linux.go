package collect

import "os"

// Debian and Ubuntu drop this file when an update needs a restart.
func rebootPending() bool {
	_, err := os.Stat("/var/run/reboot-required")
	return err == nil
}
