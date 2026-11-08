package identity

import (
	"context"
	"errors"

	"github.com/marcpope/cortendesk-agent/internal/execx"
)

func machineID(ctx context.Context) (string, error) {
	out, err := execx.Output(ctx, cmdTimeout, "/usr/sbin/ioreg", "-rd1", "-c", "IOPlatformExpertDevice")
	if err != nil {
		return "", err
	}
	if v := IORegValue(string(out), "IOPlatformUUID"); v != "" {
		return v, nil
	}
	return "", errors.New("IOPlatformUUID not found")
}
