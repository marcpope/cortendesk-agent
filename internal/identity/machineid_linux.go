package identity

import "context"

func machineID(context.Context) (string, error) {
	return readTrimmed("/etc/machine-id", "/var/lib/dbus/machine-id")
}
