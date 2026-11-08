//go:build !windows

package collect

import (
	"context"

	"github.com/shirou/gopsutil/v4/host"
)

func users(ctx context.Context) []string {
	list, err := host.UsersWithContext(ctx)
	if err != nil {
		return []string{}
	}
	names := make([]string, 0, len(list))
	for _, u := range list {
		names = append(names, u.User)
	}
	return uniqueSorted(names)
}
