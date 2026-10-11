package orchestrator

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/RakhaYandra/tora/internal/config"
	"github.com/RakhaYandra/tora/internal/health"
	"github.com/RakhaYandra/tora/internal/logs"
	"github.com/RakhaYandra/tora/internal/process"
	"github.com/RakhaYandra/tora/internal/state"
)

const (
	// StartupGrace is how long up waits after spawn to catch instant exits.
	StartupGrace = time.Second
	// MaxRestarts bounds on-failure auto restarts; backoff doubles from 1s to 30s.
	MaxRestarts = 5
)

// Result is the per-service outcome of a lifecycle operation.
type Result struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	PID      int    `json:"pid,omitempty"`
	ExitCode int    `json:"exit_code,omitempty"`
	Error    string `json:"error,omitempty"`
}

// Orchestrator drives service lifecycles for one loaded project.
type Orchestrator struct {
	L *config.Loaded
	S *state.Store
}

func (o *Orchestrator) depMap() map[string][]string {
	m := make(map[string][]string, len(o.L.Config.Services))
	for name, s := range o.L.Config.Services {
		m[name] = append([]string{}, s.DependsOn...)
	}
	return m
}

func (o *Orchestrator) serviceNames() []string {
	names := make([]string, 0, len(o.L.Config.Services))
	for n := range o.L.Config.Services {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Up starts all services (or target plus its dependencies) in dependency order.
// Independent services in one level start in parallel. On failure the services
// started by this call are rolled back in reverse order.
func (o *Orchestrator) Up(ctx context.Context, target string) []Result {
	o.S.Reconcile()
	o.reconcileRestarts(ctx)

	scope := o.depMap()
	if target != "" {
		sub, err := Subgraph(scope, target)
		if err != nil {
			return []Result{{Name: target, Status: state.Failed, Error: err.Error()}}
		}
		scope = sub
	}
	levels, err := Sort(scope)
	if err != nil {
		return []Result{{Name: target, Status: state.Failed, Error: err.Error()}}
	}
	var (
		mu      sync.Mutex
		results = map[string]Result{}
		started []string // spawn order of this call, for rollback
		failed  bool
	)
	// Skip dependents of failed services: track unsatisfied set.
	blocked := map[string]bool{}
	for _, lvl := range levels {
		if ctx.Err() != nil {
			for _, n := range o.serviceNames() {
				if _, ok := results[n]; !ok {
					if _, inScope := scope[n]; inScope {
						results[n] = Result{Name: n, Status: state.Stopped, Error: "cancelled"}
					}
				}
			}
			break
		}
		// A level runs only if its deps succeeded; filter blocked first.
		var runnable []string
		for _, n := range lvl {
			bad := blocked[n]
			if !bad {
				for _, d := range scope[n] {
					if r, ok := results[d]; !ok || (r.Status != state.Running && r.Status != state.Exited) {
						bad = true
						break
					}
				}
			}
			if bad {
				blocked[n] = true
				results[n] = Result{Name: n, Status: state.Stopped, Error: "dependency failed; not started"}
				continue
			}
			runnable = append(runnable, n)
		}
		var wg sync.WaitGroup
		for _, n := range runnable {
			wg.Add(1)
			go func(name string) {
				defer wg.Done()
				r := o.startOne(ctx, name)
				mu.Lock()
				results[name] = r
				if r.Status == state.Running || r.Status == state.Exited {
					started = append(started, name)
				} else {
					failed = true
				}
				mu.Unlock()
			}(n)
		}
		wg.Wait()
	}
	if failed {
		for i := len(started) - 1; i >= 0; i-- {
			o.stopOne(context.WithoutCancel(ctx), started[i])
		}
		for _, n := range started {
			if r := results[n]; r.Status == state.Running {
				r.Status = state.Stopped
				r.PID = 0
				r.Error = "rolled back: a sibling service failed to start"
				results[n] = r
			}
		}
	}
	out := make([]Result, 0, len(scope))
	for n := range scope {
		if r, ok := results[n]; ok {
			out = append(out, r)
		} else {
			out = append(out, Result{Name: n, Status: state.Stopped, Error: "not attempted"})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Down stops every managed service in reverse dependency order.
func (o *Orchestrator) Down(ctx context.Context) []Result {
	live := o.S.Reconcile()
	levels, err := Sort(o.depMap())
	if err != nil {
		return []Result{{Status: state.Failed, Error: err.Error()}}
	}
	var out []Result
	for _, n := range ShutdownOrder(levels) {
		if e, ok := live[n]; ok && (e.Status == state.Running || e.Status == state.Starting) {
			out = append(out, o.stopOne(ctx, n))
		} else {
			out = append(out, Result{Name: n, Status: state.Stopped})
		}
	}
	return out
}

// Start runs one service plus any of its dependencies not already running.
func (o *Orchestrator) Start(ctx context.Context, target string) []Result {
	if _, ok := o.L.Config.Services[target]; !ok {
		return []Result{{Name: target, Status: state.Failed, Error: fmt.Sprintf("unknown service %q", target)}}
	}
	o.S.Reconcile()
	o.reconcileRestarts(ctx)
	sub, _ := Subgraph(o.depMap(), target)
	levels, err := Sort(sub)
	if err != nil {
		return []Result{{Name: target, Status: state.Failed, Error: err.Error()}}
	}
	var out []Result
	for _, lvl := range levels {
		for _, n := range lvl {
			if e, ok := o.S.Get(n); ok && e.Status == state.Running && state.Owns(e.PID, e.PGID, e.StartTime) {
				out = append(out, Result{Name: n, Status: state.Running, PID: e.PID})
				continue
			}
			out = append(out, o.startOne(ctx, n))
		}
	}
	return out
}

// Stop halts one service. Refused when a dependent is still running.
func (o *Orchestrator) Stop(ctx context.Context, target string) Result {
	if _, ok := o.L.Config.Services[target]; !ok {
		return Result{Name: target, Status: state.Failed, Error: fmt.Sprintf("unknown service %q", target)}
	}
	o.S.Reconcile()
	for _, d := range Dependents(o.depMap(), target) {
		if e, ok := o.S.Get(d); ok && e.Status == state.Running {
			return Result{Name: target, Status: state.Failed,
				Error: fmt.Sprintf("dependent %q is still running; stop it first (or use down)", d)}
		}
	}
	return o.stopOne(ctx, target)
}

// RestartAll gracefully stops every managed service (reverse dependency
// order) then starts everything again. A failed start rolls back like Up.
func (o *Orchestrator) RestartAll(ctx context.Context) []Result {
	down := o.Down(ctx)
	if opFailed(down) {
		return down
	}
	return o.Up(ctx, "")
}

func opFailed(rs []Result) bool {
	for _, r := range rs {
		if r.Status == state.Failed {
			return true
		}
	}
	return false
}

// Restart gracefully stops then starts one service. Same dependent guard as Stop.
// Any pending on-failure restart counter is reset.
func (o *Orchestrator) Restart(ctx context.Context, target string) Result {
	if _, ok := o.L.Config.Services[target]; !ok {
		return Result{Name: target, Status: state.Failed, Error: fmt.Sprintf("unknown service %q", target)}
	}
	o.S.Reconcile()
	for _, d := range Dependents(o.depMap(), target) {
		if e, ok := o.S.Get(d); ok && e.Status == state.Running {
			return Result{Name: target, Status: state.Failed,
				Error: fmt.Sprintf("dependent %q is still running; stop it first (or use down)", d)}
		}
	}
	r := o.stopOne(ctx, target)
	if r.Status != state.Stopped {
		return r
	}
	if e, ok := o.S.Get(target); ok {
		e.Restarts = 0
		o.S.Set(e)
		_ = o.S.Save()
	}
	return o.startOne(ctx, target)
}

func (o *Orchestrator) startOne(ctx context.Context, name string) Result {
	svc := o.L.Config.Services[name]
	entry := state.ServiceState{
		Name: name, Command: svc.Command, WorkDir: o.L.WorkDirs[name],
		Status: state.Starting, ExitCode: -1, StartedAt: time.Now(),
	}
	if prev, ok := o.S.Get(name); ok {
		entry.Restarts = prev.Restarts
	}
	o.S.Set(entry)
	_ = o.S.Save()

	logPath := logs.Path(o.L.LogDir, name)
	if err := logs.RotateIfNeeded(logPath); err != nil {
		return o.markFailed(name, fmt.Sprintf("log rotation: %v", err))
	}
	env := map[string]string{}
	for k, v := range svc.Env {
		env[k] = v
	}
	child, err := process.Spawn(svc.Command, o.L.WorkDirs[name], env, logPath)
	if err != nil {
		return o.markFailed(name, err.Error())
	}
	// Grace window: catch instant exits to report FAILED/EXITED accurately.
	waitCh := make(chan error, 1)
	go func() { waitCh <- child.Cmd.Wait() }()
	select {
	case err := <-waitCh:
		code := 0
		if err != nil {
			code = 1
			if ee, ok := err.(interface{ ExitCode() int }); ok {
				code = ee.ExitCode()
			}
		}
		if code == 0 {
			entry.Status = state.Exited
			entry.ExitCode = 0
			entry.PID, entry.PGID, entry.StartTime, entry.Exe = child.PID, child.PGID, child.StartTime, child.Exe
			entry.Error = "process exited immediately with code 0 (one-shot?); dependents may still start"
		} else {
			return o.markFailed(name, fmt.Sprintf("process exited immediately with code %d; see %s", code, logPath))
		}
	case <-time.After(StartupGrace):
		go func() {
			<-waitCh // reap whenever it eventually exits (pre-exit reparent safe)
		}()
		entry.Status = state.Running
		entry.PID, entry.PGID, entry.StartTime, entry.Exe = child.PID, child.PGID, child.StartTime, child.Exe
	case <-ctx.Done():
		_ = child.Cmd.Process.Kill()
		<-waitCh
		return o.markFailed(name, "cancelled during startup")
	}
	o.S.Set(entry)
	_ = o.S.Save()
	if entry.Status == state.Running && svc.Healthcheck != nil {
		return o.awaitHealthy(ctx, name, entry, svc)
	}
	return Result{Name: name, Status: entry.Status, PID: entry.PID, ExitCode: entry.ExitCode, Error: entry.Error}
}

// awaitHealthy blocks until the service passes its healthcheck (budget =
// runtime.startup_timeout). Failure stops the just-spawned process and marks
// FAILED so dependents never start on an unready dependency.
func (o *Orchestrator) awaitHealthy(ctx context.Context, name string, entry state.ServiceState, svc config.Service) Result {
	wait := o.L.Config.Runtime.StartupTimeout.Std()
	if err := health.Wait(ctx, svc.Healthcheck, wait); err != nil {
		_, _ = process.Stop(entry.PID, entry.PGID, entry.StartTime, o.L.Config.Runtime.ShutdownTimeout.Std())
		if e, ok := o.S.Get(name); ok {
			e.Status = state.Failed
			e.Ready = false
			e.Error = fmt.Sprintf("unhealthy: %v; see %s", err, logs.Path(o.L.LogDir, name))
			o.S.Set(e)
			_ = o.S.Save()
		}
		return Result{Name: name, Status: state.Failed, PID: entry.PID,
			Error: fmt.Sprintf("unhealthy: %v", err)}
	}
	if e, ok := o.S.Get(name); ok {
		e.Ready = true
		o.S.Set(e)
		_ = o.S.Save()
		entry = e
	}
	return Result{Name: name, Status: entry.Status, PID: entry.PID, ExitCode: entry.ExitCode, Error: entry.Error}
}

func (o *Orchestrator) markFailed(name, msg string) Result {
	if e, ok := o.S.Get(name); ok {
		e.Status = state.Failed
		e.Error = msg
		o.S.Set(e)
		_ = o.S.Save()
	}
	return Result{Name: name, Status: state.Failed, Error: msg}
}

func (o *Orchestrator) stopOne(ctx context.Context, name string) Result {
	e, ok := o.S.Get(name)
	if !ok || e.Status == state.Stopped {
		return Result{Name: name, Status: state.Stopped}
	}
	if e.Status == state.Exited || e.Status == state.Failed {
		// Acknowledge a dead service: if nothing of ours runs anymore,
		// record STOPPED (keeps exit code/error for forensics).
		if e.PID <= 0 || !state.Owns(e.PID, e.PGID, e.StartTime) {
			e.Status = state.Stopped
			o.S.Set(e)
			_ = o.S.Save()
		}
		return Result{Name: name, Status: state.Stopped, PID: e.PID, ExitCode: e.ExitCode, Error: e.Error}
	}
	e.Status = state.Stopping
	o.S.Set(e)
	_ = o.S.Save()
	timeout := o.L.Config.Runtime.ShutdownTimeout.Std()
	killed, err := process.Stop(e.PID, e.PGID, e.StartTime, timeout)
	if err != nil {
		e.Status = state.Failed
		e.Error = err.Error()
		o.S.Set(e)
		_ = o.S.Save()
		return Result{Name: name, Status: state.Failed, PID: e.PID, Error: err.Error()}
	}
	select {
	case <-ctx.Done():
		return Result{Name: name, Status: state.Stopping, PID: e.PID, Error: "cancelled during shutdown"}
	default:
	}
	e.Status = state.Stopped
	e.Restarts = 0
	if killed {
		e.ExitCode = 137
		e.Error = "SIGTERM timeout exceeded; SIGKILL used"
	} else {
		e.ExitCode = 0
		e.Error = ""
	}
	o.S.Set(e)
	_ = o.S.Save()
	return Result{Name: name, Status: state.Stopped, PID: e.PID, ExitCode: e.ExitCode, Error: e.Error}
}

// reconcileRestarts respawns on-failure services found unexpectedly gone.
// Best-effort: without a daemon, supervision happens on tora invocations.
func (o *Orchestrator) reconcileRestarts(ctx context.Context) {
	for _, n := range o.serviceNames() {
		svc := o.L.Config.Services[n]
		if svc.Restart != "on-failure" {
			continue
		}
		e, ok := o.S.Get(n)
		if !ok || e.Status != state.Exited {
			continue
		}
		if e.Restarts >= MaxRestarts {
			continue
		}
		backoff := time.Second << e.Restarts
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		e.Restarts++
		o.S.Set(e)
		_ = o.S.Save()
		o.startOne(ctx, n)
	}
}
