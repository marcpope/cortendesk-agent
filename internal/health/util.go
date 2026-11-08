package health

import "strings"

func trimLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return strings.TrimSpace(s)
}
