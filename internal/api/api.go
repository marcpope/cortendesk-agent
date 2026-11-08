// Package api holds the JSON bodies of the console's /agent/v1 endpoints.
package api

import "encoding/json"

type EnrollRequest struct {
	Token        string `json:"token"`
	MachineUUID  string `json:"machine_uuid"`
	RustDeskID   string `json:"rustdesk_id"`
	Hostname     string `json:"hostname"`
	OS           string `json:"os"`
	OSVersion    string `json:"os_version"`
	Arch         string `json:"arch"`
	AgentVersion string `json:"agent_version"`
}

type EnrollResponse struct {
	AgentID  string `json:"agent_id"`
	Secret   string `json:"secret"`
	Interval int    `json:"interval"`
}

type Report struct {
	AgentVersion  string    `json:"agent_version"`
	MachineUUID   string    `json:"machine_uuid"`
	RustDeskID    string    `json:"rustdesk_id"`
	Hostname      string    `json:"hostname"`
	Uptime        uint64    `json:"uptime"`
	CPU           CPU       `json:"cpu"`
	Memory        Memory    `json:"memory"`
	Disks         []Disk    `json:"disks"`
	Network       NetTotals `json:"network"`
	RustDesk      RustDesk  `json:"rustdesk"`
	Users         []string  `json:"users"`
	RebootPending bool      `json:"reboot_pending"`
	InventoryHash string    `json:"inventory_hash"`
	// Health is the cached slow checks; nil until the first run finishes.
	Health *Health `json:"health,omitempty"`
	// Capabilities are the command types this agent runs.
	Capabilities []string `json:"capabilities"`
	// Trial marks the one report a new binary sends before an update swaps
	// it in. The console delivers no commands to it.
	Trial bool `json:"trial,omitempty"`
}

// Health keys are all optional: a check that does not apply or failed is
// left out, never sent as false.
type Health struct {
	Antivirus      []Antivirus  `json:"antivirus,omitempty"`
	Firewall       *bool        `json:"firewall,omitempty"`
	Encryption     []Encryption `json:"encryption,omitempty"`
	Updates        *Updates     `json:"updates,omitempty"`
	Smart          []Smart      `json:"smart,omitempty"`
	Services       []Service    `json:"services,omitempty"`
	EventErrors24h *int         `json:"event_errors_24h,omitempty"`
	WifiSSID       string       `json:"wifi_ssid,omitempty"`
	CollectedAt    int64        `json:"collected_at,omitempty"`
}

type Antivirus struct {
	Name     string `json:"name"`
	Enabled  *bool  `json:"enabled,omitempty"`
	UpToDate *bool  `json:"up_to_date,omitempty"`
}

type Encryption struct {
	Volume    string `json:"volume"`
	Encrypted bool   `json:"encrypted"`
	Method    string `json:"method,omitempty"`
}

type Updates struct {
	Missing       *int   `json:"missing,omitempty"`
	Failed        *int   `json:"failed,omitempty"`
	LastInstalled string `json:"last_installed,omitempty"`
}

type Smart struct {
	Device  string `json:"device"`
	Model   string `json:"model,omitempty"`
	Healthy bool   `json:"healthy"`
	Status  string `json:"status"`
}

// Service states.
const (
	ServiceRunning = "running"
	ServiceStopped = "stopped"
	ServiceMissing = "missing"
	ServiceOther   = "other"
)

type Service struct {
	Name    string `json:"name"`
	Display string `json:"display,omitempty"`
	State   string `json:"state"`
}

type CPU struct {
	Percent float64 `json:"percent"`
	Cores   int     `json:"cores"`
	// Load is nil on Windows, which drops the key.
	Load []float64 `json:"load,omitempty"`
}

type Memory struct {
	Total     uint64 `json:"total"`
	Used      uint64 `json:"used"`
	SwapTotal uint64 `json:"swap_total"`
	SwapUsed  uint64 `json:"swap_used"`
}

type Disk struct {
	Mount string `json:"mount"`
	FS    string `json:"fs"`
	Total uint64 `json:"total"`
	Used  uint64 `json:"used"`
}

// NetTotals are counters since boot. Packets are sent too because some
// virtual NIC drivers (QEMU's emulated e1000 on Windows) count packets but
// leave the byte counters at 0; the console uses them to tell that apart
// from an idle machine.
type NetTotals struct {
	RxBytes   uint64 `json:"rx_bytes"`
	TxBytes   uint64 `json:"tx_bytes"`
	RxPackets uint64 `json:"rx_packets"`
	TxPackets uint64 `json:"tx_packets"`
}

type RustDesk struct {
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	Version   string `json:"version"`
}

type ReportResponse struct {
	Interval      int       `json:"interval"`
	Inventory     bool      `json:"inventory"`
	WatchServices []string  `json:"watch_services"`
	Commands      []Command `json:"commands"`
}

// Command types. The agent runs these and nothing else.
const (
	CmdRustDeskUpgrade = "rustdesk.upgrade"
	CmdOSUpdate        = "os.update"
	CmdReboot          = "reboot"
	CmdAgentUpdate     = "agent.update"
)

// Command is one queued command from the console. Params are checked by
// the handler for its type.
type Command struct {
	ID     string          `json:"id"`
	Type   string          `json:"type"`
	Params json.RawMessage `json:"params"`
}

// Command result statuses.
const (
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
)

// CommandResult is posted to /agent/v1/commands/{id}.
type CommandResult struct {
	Status   string `json:"status"`
	ExitCode *int   `json:"exit_code,omitempty"`
	Output   string `json:"output,omitempty"`
	Error    string `json:"error,omitempty"`
}

type Inventory struct {
	// Hash is sha256 (hex) of this body marshalled with Hash empty, which
	// omits the key.
	Hash     string             `json:"hash,omitempty"`
	OS       OSInfo             `json:"os"`
	Hardware Hardware           `json:"hardware"`
	Network  []NetworkInterface `json:"network"`
	Software []Software         `json:"software"`
	// LocalAdmins: Administrators (Windows), admin (macOS), sudo and wheel
	// (Linux).
	LocalAdmins []string `json:"local_admins"`
	// Battery is nil on machines without one.
	Battery *Battery `json:"battery,omitempty"`
}

// Battery capacities are in mWh.
type Battery struct {
	DesignCapacity uint64 `json:"design_capacity"`
	FullCapacity   uint64 `json:"full_capacity"`
	CycleCount     uint64 `json:"cycle_count"`
}

type OSInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Build   string `json:"build"`
	Kernel  string `json:"kernel"`
}

type Hardware struct {
	Manufacturer string  `json:"manufacturer"`
	Model        string  `json:"model"`
	Serial       string  `json:"serial"`
	BIOS         string  `json:"bios"`
	CPU          string  `json:"cpu"`
	Cores        int     `json:"cores"`
	Threads      int     `json:"threads"`
	Memory       uint64  `json:"memory"`
	Drives       []Drive `json:"drives"`
}

type Drive struct {
	Model  string `json:"model"`
	Size   uint64 `json:"size"`
	Serial string `json:"serial"`
}

type NetworkInterface struct {
	Name string   `json:"name"`
	MAC  string   `json:"mac"`
	IPs  []string `json:"ips"`
}

type Software struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Publisher string `json:"publisher"`
}

type Error struct {
	Error string `json:"error"`
}
