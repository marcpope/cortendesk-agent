// Package fakeconsole is a small in-memory stand-in for the console's
// /agent/v1 API, for tests and local runs. It follows the contract in the
// console's docs/monitoring/SPEC.md.
package fakeconsole

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"github.com/marcpope/cortendesk-agent/internal/api"
)

// Modes change how /report answers.
const (
	ModeNormal   = ""
	ModeRevoked  = "revoked"  // 401 for every bearer
	ModeDisabled = "disabled" // 403 with interval 900
	ModeDown     = "down"     // drop the connection
)

type agent struct {
	id, secret, machineUUID string
	inventoryHash           string
}

// Request is one recorded call.
type Request struct {
	Path   string
	Status int
	Body   json.RawMessage
}

type Console struct {
	Server *httptest.Server

	mu       sync.Mutex
	token    string
	interval int
	mode     string
	agents   map[string]*agent // by agent id
	watch    []string
	queue    []api.Command
	results  map[string][]api.CommandResult
	requests []Request
	// OnRequest, when set, is called for every request (for logging).
	OnRequest func(Request)
}

// New starts a fake console. With tls the server uses a self-signed
// certificate for 127.0.0.1; CAPEM returns it.
func New(token string, interval int, useTLS bool) *Console {
	return NewOn(nil, token, interval, useTLS)
}

// NewOn is New on a given listener (nil picks a free loopback port).
func NewOn(l net.Listener, token string, interval int, useTLS bool) *Console {
	c := &Console{token: token, interval: interval, agents: map[string]*agent{}, results: map[string][]api.CommandResult{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /agent/v1/enroll", c.enroll)
	mux.HandleFunc("POST /agent/v1/report", c.report)
	mux.HandleFunc("POST /agent/v1/inventory", c.inventory)
	mux.HandleFunc("POST /agent/v1/commands/{id}", c.commandResult)
	c.Server = httptest.NewUnstartedServer(mux)
	if l != nil {
		c.Server.Listener.Close()
		c.Server.Listener = l
	}
	if useTLS {
		c.Server.StartTLS()
	} else {
		c.Server.Start()
	}
	return c
}

func (c *Console) URL() string { return c.Server.URL }
func (c *Console) Close()      { c.Server.Close() }

// CAPEM is the server certificate in PEM form, for --ca-file.
func (c *Console) CAPEM() []byte {
	cert := c.Server.Certificate()
	if cert == nil {
		return nil
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
}

func (c *Console) SetMode(m string) {
	c.mu.Lock()
	c.mode = m
	c.mu.Unlock()
}

// SetWatch sets the watch_services list sent in report responses.
func (c *Console) SetWatch(names []string) {
	c.mu.Lock()
	c.watch = names
	c.mu.Unlock()
}

// Queue adds a command for the next (non-trial) report.
func (c *Console) Queue(cmd api.Command) {
	c.mu.Lock()
	c.queue = append(c.queue, cmd)
	c.mu.Unlock()
}

// Queued is how many commands wait for delivery.
func (c *Console) Queued() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.queue)
}

// Results returns the results posted for command id, in order.
func (c *Console) Results(id string) []api.CommandResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]api.CommandResult(nil), c.results[id]...)
}

func (c *Console) SetInterval(sec int) {
	c.mu.Lock()
	c.interval = sec
	c.mu.Unlock()
}

// Requests returns the recorded calls to path ("" for all).
func (c *Console) Requests(path string) []Request {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []Request
	for _, r := range c.requests {
		if path == "" || r.Path == path {
			out = append(out, r)
		}
	}
	return out
}

// ForgetInventory clears every stored inventory hash, so the next report is
// asked for a fresh one.
func (c *Console) ForgetInventory() {
	c.mu.Lock()
	for _, a := range c.agents {
		a.inventoryHash = ""
	}
	c.mu.Unlock()
}

func (c *Console) record(r *http.Request, status int, body []byte) {
	req := Request{Path: r.URL.Path, Status: status, Body: append(json.RawMessage(nil), body...)}
	c.requests = append(c.requests, req)
	if c.OnRequest != nil {
		c.OnRequest(req)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (c *Console) enroll(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	c.mu.Lock()
	defer c.mu.Unlock()
	var req struct {
		Token       string `json:"token"`
		MachineUUID string `json:"machine_uuid"`
		Hostname    string `json:"hostname"`
		OS          string `json:"os"`
	}
	if json.Unmarshal(body, &req) != nil || req.MachineUUID == "" || req.Hostname == "" || req.OS == "" {
		c.record(r, 422, body)
		writeJSON(w, 422, map[string]string{"error": "validation"})
		return
	}
	if req.Token != c.token {
		c.record(r, 401, body)
		writeJSON(w, 401, map[string]string{"error": "invalid_token"})
		return
	}
	// Same machine: reuse the row, rotate the secret.
	var a *agent
	for _, x := range c.agents {
		if x.machineUUID == req.MachineUUID {
			a = x
		}
	}
	if a == nil {
		a = &agent{id: "01J" + strings.ToUpper(randHex(12)), machineUUID: req.MachineUUID}
		c.agents[a.id] = a
	}
	a.secret = randHex(20)
	c.record(r, 201, body)
	writeJSON(w, 201, map[string]any{"agent_id": a.id, "secret": a.secret, "interval": c.interval})
}

// auth returns the agent for the bearer, or nil.
func (c *Console) auth(r *http.Request) *agent {
	h, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return nil
	}
	id, secret, ok := strings.Cut(h, ".")
	if !ok {
		return nil
	}
	a := c.agents[id]
	if a == nil || a.secret != secret {
		return nil
	}
	return a
}

func (c *Console) drop(w http.ResponseWriter) bool {
	if c.mode != ModeDown {
		return false
	}
	if hj, ok := w.(http.Hijacker); ok {
		if conn, _, err := hj.Hijack(); err == nil {
			conn.Close()
			return true
		}
	}
	writeJSON(w, 503, map[string]string{"error": "down"})
	return true
}

func (c *Console) report(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.drop(w) {
		c.record(r, 0, body)
		return
	}
	a := c.auth(r)
	if a == nil || c.mode == ModeRevoked {
		c.record(r, 401, body)
		writeJSON(w, 401, map[string]string{"error": "revoked"})
		return
	}
	if c.mode == ModeDisabled {
		c.record(r, 403, body)
		writeJSON(w, 403, map[string]any{"error": "disabled", "interval": 900})
		return
	}
	var rep struct {
		InventoryHash string `json:"inventory_hash"`
		Trial         bool   `json:"trial"`
	}
	if json.Unmarshal(body, &rep) != nil {
		c.record(r, 422, body)
		writeJSON(w, 422, map[string]string{"error": "validation"})
		return
	}
	c.record(r, 200, body)
	resp := map[string]any{
		"interval":       c.interval,
		"inventory":      a.inventoryHash == "" || rep.InventoryHash != a.inventoryHash,
		"watch_services": append([]string{}, c.watch...),
		"future":         "unknown keys must be ignored",
	}
	if len(c.queue) > 0 && !rep.Trial {
		resp["commands"] = c.queue
		c.queue = nil
	}
	writeJSON(w, 200, resp)
}

func (c *Console) commandResult(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.drop(w) {
		c.record(r, 0, body)
		return
	}
	if c.auth(r) == nil {
		c.record(r, 401, body)
		writeJSON(w, 401, map[string]string{"error": "revoked"})
		return
	}
	var res api.CommandResult
	if json.Unmarshal(body, &res) != nil || res.Status == "" {
		c.record(r, 422, body)
		writeJSON(w, 422, map[string]string{"error": "invalid"})
		return
	}
	id := r.PathValue("id")
	c.results[id] = append(c.results[id], res)
	c.record(r, 200, body)
	writeJSON(w, 200, map[string]any{})
}

func (c *Console) inventory(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	c.mu.Lock()
	defer c.mu.Unlock()
	a := c.auth(r)
	if a == nil {
		c.record(r, 401, body)
		writeJSON(w, 401, map[string]string{"error": "revoked"})
		return
	}
	hash, err := VerifyHash(body)
	if err != nil {
		c.record(r, 422, body)
		writeJSON(w, 422, map[string]string{"error": err.Error()})
		return
	}
	a.inventoryHash = hash
	c.record(r, 200, body)
	writeJSON(w, 200, map[string]any{})
}

// VerifyHash recomputes the inventory hash the way a console would: drop
// "hash", sort keys, encode without whitespace or HTML escaping, sha256.
func VerifyHash(body []byte) (string, error) {
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	var m map[string]any
	if err := d.Decode(&m); err != nil {
		return "", fmt.Errorf("bad json")
	}
	claimed, _ := m["hash"].(string)
	delete(m, "hash")
	var buf bytes.Buffer
	e := json.NewEncoder(&buf)
	e.SetEscapeHTML(false)
	_ = e.Encode(m)
	sum := sha256.Sum256(bytes.TrimSuffix(buf.Bytes(), []byte("\n")))
	if got := hex.EncodeToString(sum[:]); got != claimed {
		return "", fmt.Errorf("hash_mismatch")
	}
	return claimed, nil
}
