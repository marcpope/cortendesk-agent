package inventory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/marcpope/cortendesk-agent/internal/api"
)

func TestHashIsBodyWithoutHash(t *testing.T) {
	inv := api.Inventory{
		OS:          api.OSInfo{Name: "macOS", Version: "15.1"},
		Hardware:    api.Hardware{Drives: []api.Drive{}},
		Network:     []api.NetworkInterface{},
		Software:    []api.Software{{Name: "A", Version: "1"}},
		LocalAdmins: []string{"marc"},
	}
	inv.Hash = Hash(inv)

	// What the console does: take the posted body, drop "hash", re-encode
	// with sorted keys.
	b, _ := json.Marshal(inv)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if _, ok := m["hash"]; !ok {
		t.Fatal("hash missing from body")
	}
	delete(m, "hash")
	b2, _ := json.Marshal(m)
	want := `{"hardware":{"bios":"","cores":0,"cpu":"","drives":[],"manufacturer":"","memory":0,"model":"","serial":"","threads":0},"local_admins":["marc"],"network":[],"os":{"build":"","kernel":"","name":"macOS","version":"15.1"},"software":[{"name":"A","publisher":"","version":"1"}]}`
	if string(b2) != want {
		t.Fatalf("canonical form changed:\n%s", b2)
	}
	sum := sha256.Sum256(b2)
	if inv.Hash != hex.EncodeToString(sum[:]) {
		t.Fatal("hash does not match body without hash")
	}
	if Hash(inv) != inv.Hash {
		t.Fatal("hash not stable")
	}
}

func TestNormalize(t *testing.T) {
	in := []api.Software{
		{Name: " zeta ", Version: "1"},
		{Name: "Alpha", Version: "2"},
		{Name: "alpha", Version: "1"},
		{Name: "Alpha", Version: "2"},
		{Name: ""},
	}
	out := Normalize(in)
	got := fmt.Sprint(out)
	want := "[{alpha 1 } {Alpha 2 } {zeta 1 }]"
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}

	many := make([]api.Software, MaxSoftware+50)
	for i := range many {
		many[i] = api.Software{Name: fmt.Sprintf("pkg%05d", i)}
	}
	if n := len(Normalize(many)); n != MaxSoftware {
		t.Fatalf("cap: got %d", n)
	}
}

func TestParseOSRelease(t *testing.T) {
	m := ParseOSRelease("NAME=\"Ubuntu\"\nVERSION_ID=\"24.04\"\n# comment\nID=ubuntu\nPRETTY_NAME='Ubuntu 24.04.1 LTS'\n")
	if m["NAME"] != "Ubuntu" || m["VERSION_ID"] != "24.04" || m["ID"] != "ubuntu" || m["PRETTY_NAME"] != "Ubuntu 24.04.1 LTS" {
		t.Fatalf("%v", m)
	}
}

func TestParseDpkgRPM(t *testing.T) {
	d := ParseDpkg("ii \tbash\t5.2.21-2ubuntu4\tUbuntu Developers <x@y>\nrc \told\t1.0\tSomeone\n")
	if len(d) != 1 || d[0].Name != "bash" || d[0].Version != "5.2.21-2ubuntu4" {
		t.Fatalf("%v", d)
	}
	r := ParseRPM("bash\t5.1.8-9.el9\tRed Hat, Inc.\ngpg-pubkey\tabc-def\t(none)\nfoo\t1-1\t(none)\n")
	if len(r) != 2 || r[1].Publisher != "" || r[0].Publisher != "Red Hat, Inc." {
		t.Fatalf("%v", r)
	}
}

func TestParseSystemProfiler(t *testing.T) {
	out := []byte(`{"SPHardwareDataType":[{"machine_name":"Mac Studio","machine_model":"Mac17,15","serial_number":"SER1","boot_rom_version":"1.2.3"}],
"SPNVMeDataType":[{"_items":[{"_name":"APPLE SSD","device_model":"APPLE SSD AP1024Z","device_serial":"abc","size_in_bytes":1000555581440}]}],
"SPSerialATADataType":[]}`)
	h := parseSystemProfiler(out, api.Hardware{Manufacturer: "Apple Inc.", Drives: []api.Drive{}})
	if h.Model != "Mac Studio (Mac17,15)" || h.Serial != "SER1" || h.BIOS != "1.2.3" {
		t.Fatalf("%+v", h)
	}
	if len(h.Drives) != 1 || h.Drives[0].Size != 1000555581440 || h.Drives[0].Model != "APPLE SSD AP1024Z" {
		t.Fatalf("%+v", h.Drives)
	}
}

func TestOSVersion(t *testing.T) {
	if got := OSVersion(api.OSInfo{Name: "Windows Server 2022 Standard", Version: "21H2", Build: "20348.2700"}); got != "Windows Server 2022 Standard 21H2 (20348.2700)" {
		t.Fatal(got)
	}
	if got := OSVersion(api.OSInfo{Name: "Ubuntu", Version: "24.04"}); got != "Ubuntu 24.04" {
		t.Fatal(got)
	}
}

func TestLocalAdminParsers(t *testing.T) {
	if got := fmt.Sprint(ParseDsclMembers("GroupMembership: root marc\n")); got != "[root marc]" {
		t.Error(got)
	}
	g := "root:x:0:\nsudo:x:27:marc,ops\nwheel:x:10:admin\nusers:x:100:bob\n"
	if got := fmt.Sprint(uniqueSorted(ParseGroupMembers(g, "sudo", "wheel"))); got != "[admin marc ops]" {
		t.Error(got)
	}
}

func TestMacBattery(t *testing.T) {
	if MacBattery(`"BatteryInstalled" = No`+"\n"+`"DesignCapacity" = 0`) != nil {
		t.Fatal("no battery should be nil")
	}
	out := `"BatteryInstalled" = Yes
      "DesignCapacity" = 5000
      "AppleRawMaxCapacity" = 4500
      "Voltage" = 12000
      "CycleCount" = 212`
	b := MacBattery(out)
	if b == nil || b.DesignCapacity != 60000 || b.FullCapacity != 54000 || b.CycleCount != 212 {
		t.Fatalf("%+v", b)
	}
}
