package identity

import "strings"

// IORegValue pulls a quoted string property out of `ioreg` text output:
//
//	"IOPlatformUUID" = "7FD9FB29-..."
func IORegValue(out, key string) string {
	needle := `"` + key + `" = "`
	i := strings.Index(out, needle)
	if i < 0 {
		return ""
	}
	rest := out[i+len(needle):]
	j := strings.IndexByte(rest, '"')
	if j < 0 {
		return ""
	}
	return rest[:j]
}
