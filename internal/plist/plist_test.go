package plist

import "testing"

func TestParse(t *testing.T) {
	for _, f := range []string{"testdata/info.xml", "testdata/info.bin"} {
		m, err := ReadFile(f)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if got := BundleName(m, "x"); got != "Sample Äpp" {
			t.Errorf("%s: name %q", f, got)
		}
		if got := BundleVersion(m); got != "1.4.3" {
			t.Errorf("%s: version %q", f, got)
		}
		if _, ok := m["CFBundleTypeName"]; ok {
			t.Errorf("%s: nested key leaked to top level", f)
		}
		if m["LSMinimumSystemVersion"] != "10.13" {
			t.Errorf("%s: key after array/bool lost: %v", f, m)
		}
	}
}

func TestParseGarbage(t *testing.T) {
	for _, b := range [][]byte{nil, []byte("bplist00"), []byte("bplist00" + string(make([]byte, 40))), []byte("<plist><dict><key>a")} {
		_, _ = Parse(b) // must not panic
	}
}
