package identity

import (
	"context"
	"encoding/base64"
	"testing"
)

func TestParseID(t *testing.T) {
	cases := map[string]string{
		"123456789\n":                    "123456789",
		"some log line\r\n987654321\r\n": "987654321",
		"":                               "",
		"error: no service\n":            "",
		"my_custom-id\n\n":               "my_custom-id",
	}
	for in, want := range cases {
		if got := parseID(in); got != want {
			t.Errorf("parseID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseVersion(t *testing.T) {
	if got := parseVersion("rustdesk 1.4.3\n"); got != "1.4.3" {
		t.Errorf("got %q", got)
	}
	if got := parseVersion("nothing"); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestIORegValue(t *testing.T) {
	out := `+-o Mac  <class IOPlatformExpertDevice>
    {
      "IOPlatformSerialNumber" = "ABC123"
      "IOPlatformUUID" = "7FD9FB29-665A-5555-9C67-13A97BD2A5D5"
    }`
	if got := IORegValue(out, "IOPlatformUUID"); got != "7FD9FB29-665A-5555-9C67-13A97BD2A5D5" {
		t.Errorf("got %q", got)
	}
	if got := IORegValue(out, "Missing"); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestMachineUUIDIsBase64OfID(t *testing.T) {
	u, err := MachineUUID(context.Background())
	if err != nil {
		t.Skipf("no machine id here: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(u)
	if err != nil || len(raw) == 0 {
		t.Fatalf("not base64: %q", u)
	}
	id, _ := machineID(context.Background())
	if string(raw) != id {
		t.Fatalf("decoded %q, machine id %q", raw, id)
	}
}
