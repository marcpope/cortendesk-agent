package semver

import "testing"

func TestCompare(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"1.5.1", "1.5.0", 1},
		{"1.5.0", "1.5.0", 0},
		{"1.4.9", "1.10.0", -1},
		{"0.2.0-beta.1", "0.2.0", -1},
		{"0.2.0-beta.2", "0.2.0-beta.10", -1},
		{"0.2.0-alpha", "0.2.0-beta", -1},
		{"0.2.0-beta", "0.2.0-beta.1", -1},
		{"junk", "0.0.1", -1},
	} {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
		if got := Compare(c.b, c.a); got != -c.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", c.b, c.a, got, -c.want)
		}
	}
	if Valid("1.5") || Valid("1.5.1 ") || !Valid("10.0.0-rc.1") {
		t.Error("Valid")
	}
}
