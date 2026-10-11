// Integration tests drive the real orchestrator with real Linux processes.
// No mocks for spawn, signals, or process groups.
package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/RakhaYandra/tora/internal/config"
	"github.com/RakhaYandra/tora/internal/orchestrator"
	"github.com/RakhaYandra/tora/internal/process"
	"github.com/RakhaYandra/tora/internal/state"
)

func setup(t *testing.T, yamlBody string) (*orchestrator.Orchestrator, context.Context) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "tora.yaml")
	if err := os.WriteFile(cfgPath, []byte(yamlBody), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	l, err := config.Resolve(cfg, dir)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	st, err := state.Open(l.StateDir, cfg.Project.Name)
	if err != nil {
		t.Fatal(err)
	}
	o := &orchestrator.Orchestrator{L: l, S: st}
	t.Cleanup(func() {
		o.Down(context.WithoutCancel(context.Background()))
	})
	return o, context.Background()
}

const base = `
version: 1
project:
  name: testproj
runtime:
  shutdown_timeout: 3s
  log_dir: .tora/logs
  state_dir: .tora
services:
`

func statusOf(results []orchestrator.Result, name string) orchestrator.Result {
	for _, r := range results {
		if r.Name == name {
			return r
		}
	}
	return orchestrator.Result{Name: name, Status: "MISSING"}
}

func TestUpDown(t *testing.T) {
	o, ctx := setup(t, base+`
  db:
    command: ["sleep", "30"]
  api:
    command: ["sleep", "30"]
    depends_on: ["db"]
`)
	res := o.Up(ctx, "")
	if got := statusOf(res, "db").Status; got != state.Running {
		t.Fatalf("db = %v", statusOf(res, "db"))
	}
	if got := statusOf(res, "api").Status; got != state.Running {
		t.Fatalf("api = %v", statusOf(res, "api"))
	}
	db, _ := o.S.Get("db")
	if db.PGID != db.PID {
		t.Fatalf("service must lead its own process group: %+v", db)
	}
	if !state.Owns(db.PID, db.PGID, db.StartTime) {
		t.Fatal("just-spawned process must verify as owned")
	}
	down := o.Down(ctx)
	if statusOf(down, "db").Status != state.Stopped || statusOf(down, "api").Status != state.Stopped {
		t.Fatalf("down = %v", down)
	}
	if process.GroupAlive(db.PGID) {
		t.Fatal("process group must be gone after down")
	}
}

func TestDependencyStartOrder(t *testing.T) {
	dir := t.TempDir()
	order := filepath.Join(dir, "order.txt")
	yamlBody := base + `
  api:
    command: ["sh", "-c", "echo api >> ` + order + `; exec sleep 30"]
    depends_on: ["db"]
  db:
    command: ["sh", "-c", "echo db >> ` + order + `; exec sleep 30"]
`
	cfgPath := filepath.Join(dir, "tora.yaml")
	os.WriteFile(cfgPath, []byte(yamlBody), 0o644)
	cfg, _ := config.Load(cfgPath)
	l, err := config.Resolve(cfg, dir)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := state.Open(l.StateDir, "testproj")
	o := &orchestrator.Orchestrator{L: l, S: st}
	t.Cleanup(func() { o.Down(context.Background()) })
	o.Up(context.Background(), "")
	raw, _ := os.ReadFile(order)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 || lines[0] != "db" || lines[1] != "api" {
		t.Fatalf("start order = %q, want [db api]", lines)
	}
	// sh wrappers exec into sleep: identity must survive the exec and
	// Down must reap every group (regression: exe-based identity leaked these).
	down := o.Down(context.Background())
	if statusOf(down, "db").Status != state.Stopped || statusOf(down, "api").Status != state.Stopped {
		t.Fatalf("down = %v", down)
	}
	for _, n := range []string{"db", "api"} {
		e, _ := o.S.Get(n)
		if process.GroupAlive(e.PGID) {
			t.Fatalf("group %d of %s must be gone after down", e.PGID, n)
		}
	}
}

func TestFailedDepBlocksAndRollsBack(t *testing.T) {
	o, ctx := setup(t, base+`
  bad:
    command: ["false"]
  needsbad:
    command: ["sleep", "30"]
    depends_on: ["bad"]
  good:
    command: ["sleep", "30"]
`)
	res := o.Up(ctx, "")
	if statusOf(res, "bad").Status != state.Failed {
		t.Fatalf("bad must fail: %v", res)
	}
	if got := statusOf(res, "needsbad"); got.Status != state.Stopped || got.Error == "" {
		t.Fatalf("dependent must stay stopped with reason: %+v", got)
	}
	// good was rolled back: no sleep may survive.
	entries := o.S.All()
	for n, e := range entries {
		if e.Status == state.Running {
			t.Fatalf("%s still running after rollback", n)
		}
	}
}

func TestStopGuardAndRestart(t *testing.T) {
	o, ctx := setup(t, base+`
  db:
    command: ["sleep", "30"]
  api:
    command: ["sleep", "30"]
    depends_on: ["db"]
`)
	o.Up(ctx, "")
	if r := o.Stop(ctx, "db"); r.Status != state.Failed || !strings.Contains(r.Error, "api") {
		t.Fatalf("stop db must refuse while api runs: %+v", r)
	}
	before, _ := o.S.Get("api")
	r := o.Restart(ctx, "db")
	_ = r
	// db has dependent api running -> restart refused too
	if r.Status != state.Failed {
		t.Fatalf("restart db must refuse while api runs: %+v", r)
	}
	if r := o.Restart(ctx, "api"); r.Status != state.Running {
		t.Fatalf("restart api: %+v", r)
	}
	after, _ := o.S.Get("api")
	if before.PID == after.PID {
		t.Fatal("restart must spawn a new PID")
	}
	if state.Inspect(before.PID).Alive {
		t.Fatal("old PID must be dead after restart")
	}
	if r := o.Stop(ctx, "api"); r.Status != state.Stopped {
		t.Fatalf("stop api: %+v", r)
	}
	if r := o.Stop(ctx, "db"); r.Status != state.Stopped {
		t.Fatalf("stop db after dependent stopped: %+v", r)
	}
}

func TestSIGKILLFallback(t *testing.T) {
	o, ctx := setup(t, `
version: 1
project:
  name: stub
runtime:
  shutdown_timeout: 1s
  log_dir: .tora/logs
  state_dir: .tora
services:
  stubborn:
    command: ["sh", "-c", "trap '' TERM; while true; do sleep 1; done"]
`)
	o.Up(ctx, "")
	start := time.Now()
	down := o.Down(ctx)
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Fatalf("shutdown took too long: %v", elapsed)
	}
	r := statusOf(down, "stubborn")
	if r.Status != state.Stopped || r.ExitCode != 137 {
		t.Fatalf("stubborn must be SIGKILLed (137): %+v", r)
	}
	e, _ := o.S.Get("stubborn")
	if process.GroupAlive(e.PGID) {
		t.Fatal("stubborn group must be gone")
	}
}

func TestStaleStateNeverKillsForeign(t *testing.T) {
	o, ctx := setup(t, base+`
  db:
    command: ["sleep", "30"]
`)
	// Plant a foreign RUNNING entry: PID 1 can never be ours (starttime mismatch).
	e := state.ServiceState{Name: "evil", PID: 1, PGID: 1, StartTime: "0", Exe: "nope",
		Status: state.Running, StartedAt: time.Now()}
	o.S.Set(e)
	_ = o.S.Save()
	_ = ctx
	live := o.S.Reconcile()
	if _, ok := live["evil"]; ok {
		t.Fatal("foreign entry must be pruned")
	}
	var st syscall.WaitStatus
	_ = st
	if !state.Inspect(1).Alive {
		t.Fatal("PID 1 must still be alive (sanity)")
	}
}

func TestUnexpectedExitDetected(t *testing.T) {
	o, ctx := setup(t, base+`
  db:
    command: ["sleep", "30"]
  oneshot:
    command: ["true"]
`)
	res := o.Up(ctx, "")
	if statusOf(res, "oneshot").Status != state.Exited {
		t.Fatalf("oneshot must be EXITED: %v", res)
	}
	db, _ := o.S.Get("db")
	syscall.Kill(db.PID, syscall.SIGTERM)
	deadline := time.Now().Add(5 * time.Second)
	for state.Inspect(db.PID).Alive && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	live := o.S.Reconcile()
	if live["db"].Status != state.Exited {
		t.Fatalf("externally killed service must be EXITED: %+v", live["db"])
	}
}

func TestLogsCaptureStdoutStderr(t *testing.T) {
	o, ctx := setup(t, base+`
  noisy:
    command: ["sh", "-c", "echo out-line; echo err-line >&2; exec sleep 30"]
`)
	o.Up(ctx, "")
	logPath := o.L.LogDir + "/noisy.log"
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		raw, _ := os.ReadFile(logPath)
		if strings.Contains(string(raw), "out-line") && strings.Contains(string(raw), "err-line") {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	raw, _ := os.ReadFile(logPath)
	t.Fatalf("log must contain both streams, got %q", string(raw))
}

func TestUnknownService(t *testing.T) {
	o, ctx := setup(t, base+`
  db:
    command: ["sleep", "30"]
`)
	if r := o.Stop(ctx, "ghost"); r.Status != state.Failed {
		t.Fatalf("unknown stop must fail: %+v", r)
	}
	res := o.Up(ctx, "ghost")
	if len(res) != 1 || res[0].Status != state.Failed {
		t.Fatalf("unknown up target must fail: %+v", res)
	}
}
