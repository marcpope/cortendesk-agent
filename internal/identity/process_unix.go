//go:build !windows

package identity

import (
	"strings"

	"github.com/shirou/gopsutil/v4/process"
)

func processRunning(names []string) bool {
	procs, err := process.Processes()
	if err != nil {
		return false
	}
	for _, p := range procs {
		n, err := p.Name()
		if err != nil {
			continue
		}
		for _, want := range names {
			if strings.EqualFold(n, want) {
				return true
			}
		}
	}
	return false
}
