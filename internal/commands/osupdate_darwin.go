package commands

import (
	"context"
	"strings"

	"github.com/marcpope/cortendesk-agent/internal/execx"
)

// softwareupdate on Apple silicon can't install macOS updates without a
// volume owner signing in; it says so in the output, which reaches the
// console.
func installUpdates(ctx context.Context, out *Output) (bool, error) {
	if err := execx.Run(ctx, osUpdateTimeout, out, nil, "/usr/sbin/softwareupdate", "--install", "--all", "--agree-to-license"); err != nil {
		return false, err
	}
	s := strings.ToLower(out.String())
	return strings.Contains(s, "restart") && !strings.Contains(s, "no updates are available"), nil
}
