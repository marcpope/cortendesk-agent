// Package commands runs the fixed set of commands the console can queue
// (docs/monitoring/SPEC.md, "Commands"). There is no generic "run this":
// each type has a handler compiled in, and every handler checks its own
// params.
package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/marcpope/cortendesk-agent/internal/api"
	"github.com/marcpope/cortendesk-agent/internal/client"
)

// MaxOutput is how much of a command's output goes back to the console:
// the first 4 KB (what was downloaded, what was verified) and the last 12 KB
// (how it ended).
const MaxOutput = 16 << 10

const outputHead = 4 << 10

// ErrResultLater tells the runner a handler will report its result some
// other way (agent.update: the new binary or the rollback sends it).
var ErrResultLater = errors.New("result sent later")

// Handler runs one command. It writes progress to out and returns nil on
// success. A params error should say what is wrong; it goes to the console.
type Handler func(ctx context.Context, params json.RawMessage, out *Output) error

// Spec is a handler and how long it may run.
type Spec struct {
	Run     Handler
	Timeout time.Duration
	// After runs once the handler returns, whatever the result. Optional.
	After func()
}

// Runner takes commands from report responses and runs them one at a time
// in the background, so reports keep going during a long update.
type Runner struct {
	Log      *slog.Logger
	Store    *Store
	Handlers map[string]Spec
	// Post sends a result to the console.
	Post func(ctx context.Context, id string, r api.CommandResult) error

	queue chan api.Command
	once  sync.Once
	// busy is true while a command runs, for tests.
	busy sync.WaitGroup
}

func (r *Runner) init() {
	r.once.Do(func() { r.queue = make(chan api.Command, 64) })
}

// Capabilities lists the command types this agent runs, sorted.
func (r *Runner) Capabilities() []string {
	out := make([]string, 0, len(r.Handlers))
	for t := range r.Handlers {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// Offer queues commands from a report response. A command id seen before is
// skipped, so a redelivered command never runs twice. An unknown type fails
// straight away.
func (r *Runner) Offer(ctx context.Context, cmds []api.Command) {
	r.init()
	for _, c := range cmds {
		if c.ID == "" || !r.Store.MarkSeen(c.ID) {
			continue
		}
		if _, ok := r.Handlers[c.Type]; !ok {
			r.finish(ctx, c.ID, api.CommandResult{Status: api.StatusFailed, Error: "this agent does not run " + quote(c.Type)})
			continue
		}
		r.busy.Add(1)
		select {
		case r.queue <- c:
		default:
			r.busy.Done()
			r.finish(ctx, c.ID, api.CommandResult{Status: api.StatusFailed, Error: "too many queued commands"})
		}
	}
}

// Run executes queued commands until ctx is cancelled.
func (r *Runner) Run(ctx context.Context) {
	r.init()
	for {
		select {
		case <-ctx.Done():
			return
		case c := <-r.queue:
			r.execute(ctx, c)
			r.busy.Done()
		}
	}
}

// Wait blocks until every queued command has finished. For tests.
func (r *Runner) Wait() { r.busy.Wait() }

// Drain runs whatever is queued on this goroutine and returns. For tests
// that drive the agent step by step.
func (r *Runner) Drain(ctx context.Context) {
	r.init()
	for {
		select {
		case c := <-r.queue:
			r.execute(ctx, c)
			r.busy.Done()
		default:
			return
		}
	}
}

func (r *Runner) execute(ctx context.Context, c api.Command) {
	spec := r.Handlers[c.Type]
	r.Log.Info("command started", "id", c.ID, "type", c.Type)
	if err := r.Post(ctx, c.ID, api.CommandResult{Status: api.StatusRunning}); err != nil {
		r.Log.Warn("could not report command start", "id", c.ID, "err", err)
	}

	out := &Output{}
	runCtx, cancel := context.WithTimeout(context.WithValue(ctx, idKey{}, c.ID), spec.Timeout)
	err := safeRun(runCtx, spec.Run, c.Params, out)
	cancel()
	if spec.After != nil {
		spec.After()
	}

	if errors.Is(err, ErrResultLater) {
		r.Log.Info("command handed off", "id", c.ID, "type", c.Type)
		return
	}
	res := api.CommandResult{Status: api.StatusSucceeded, Output: out.String()}
	if err != nil {
		res.Status = api.StatusFailed
		res.Error = truncate(err.Error(), 1000)
		if runCtx.Err() == context.DeadlineExceeded {
			res.Error = fmt.Sprintf("timed out after %s", spec.Timeout)
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code := ee.ExitCode()
			res.ExitCode = &code
		}
	}
	r.Log.Info("command finished", "id", c.ID, "type", c.Type, "status", res.Status, "err", res.Error)
	r.finish(ctx, c.ID, res)
}

// safeRun turns a handler panic into an error, so one bad command can't take
// the agent down.
func safeRun(ctx context.Context, h Handler, params json.RawMessage, out *Output) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("internal error: %v", p)
		}
	}()
	return h(ctx, params, out)
}

// finish posts a final result, or keeps it for Flush when the console can't
// be reached.
func (r *Runner) finish(ctx context.Context, id string, res api.CommandResult) {
	if err := r.Post(ctx, id, res); err != nil {
		r.Log.Warn("could not send command result, will retry", "id", id, "err", err)
		r.Store.AddPending(id, res)
	}
}

// Flush resends results the console has not accepted yet. A result the
// console rejects with 4xx (command deleted) is dropped.
func (r *Runner) Flush(ctx context.Context) {
	for _, p := range r.Store.Pending() {
		err := r.Post(ctx, p.ID, p.Result)
		switch status := client.StatusOf(err); {
		case err == nil:
			r.Store.RemovePending(p.ID)
		case status >= 400 && status < 500:
			r.Log.Warn("console refused a command result, dropping it", "id", p.ID, "err", err)
			r.Store.RemovePending(p.ID)
		default:
			return
		}
	}
}

type idKey struct{}

// IDFrom returns the id of the command a handler is running.
func IDFrom(ctx context.Context) string {
	id, _ := ctx.Value(idKey{}).(string)
	return id
}

// Output keeps the start and the end of what is written to it, MaxOutput
// in all.
type Output struct {
	mu   sync.Mutex
	head []byte
	tail []byte
	cut  bool
}

func (o *Output) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := len(p)
	if room := outputHead - len(o.head); room > 0 {
		k := min(room, len(p))
		o.head = append(o.head, p[:k]...)
		p = p[k:]
	}
	o.tail = append(o.tail, p...)
	if over := len(o.tail) - (MaxOutput - outputHead); over > 0 {
		o.tail = slices.Clone(o.tail[over:])
		o.cut = true
	}
	return n, nil
}

// Printf writes a line.
func (o *Output) Printf(format string, args ...any) {
	fmt.Fprintf(o, format+"\n", args...)
}

func (o *Output) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	sep := ""
	if o.cut {
		sep = "\n...\n"
	}
	return strings.ToValidUTF8(string(o.head)+sep+string(o.tail), "?")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + "..."
}

func quote(s string) string {
	return fmt.Sprintf("%q", truncate(s, 64))
}

// decode reads params strictly: unknown keys are an error, so a console
// typo or a newer field this agent doesn't understand fails loudly.
func decode(params json.RawMessage, v any) error {
	if len(params) == 0 {
		params = json.RawMessage("{}")
	}
	d := json.NewDecoder(strings.NewReader(string(params)))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return fmt.Errorf("bad params: %w", err)
	}
	return nil
}
