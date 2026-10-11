// Package daemon provides continuous foreground supervision: stale
// reconciliation, on-failure respawn with backoff, and health re-probes.
// It holds the project lock for its lifetime; other mutating commands fail
// fast while it runs. Exiting never stops services unless DownOnExit set.
package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/RakhaYandra/tora/internal/config"
	"github.com/RakhaYandra/tora/internal/health"
	"github.com/RakhaYandra/tora/internal/orchestrator"
	"github.com/RakhaYandra/tora/internal/state"
)

// Daemon supervises one loaded project on a tick loop.
type Daemon struct {
	L          *config.Loaded
	Interval   time.Duration
	DownOnExit bool
	// OnEvent reports activity (stdout wiring in CLI, capture in tests).
	OnEvent func(string)

	nextRetry map[string]time.Time
	logFile   *os.File
}

func (d *Daemon) say(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if d.OnEvent != nil {
		d.OnEvent(msg)
	}
	if d.logFile != nil {
		fmt.Fprintf(d.logFile, "%s %s\n", time.Now().Format(time.RFC3339), msg)
	}
}

// Run blocks until ctx ends. On signal exit the caller decides (130);
// services are stopped only if DownOnExit is set.
func (d *Daemon) Run(ctx context.Context) error {
	lock, err := state.Acquire(d.L.StateDir)
	if err != nil {
		return err
	}
	defer lock.Release()
	lf, err := os.OpenFile(filepath.Join(d.L.StateDir, "daemon.log"),
		os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open daemon log: %w", err)
	}
	defer lf.Close()
	d.logFile = lf
	d.nextRetry = map[string]time.Time{}
	if d.Interval <= 0 {
		d.Interval = 5 * time.Second
	}
	d.say("daemon watching project %s every %s", d.L.Config.Project.Name, d.Interval)
	tick := time.NewTicker(d.Interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			if d.DownOnExit {
				d.say("shutting down all services")
				st, _ := state.Open(d.L.StateDir, d.L.Config.Project.Name)
				o := &orchestrator.Orchestrator{L: d.L, S: st}
				for _, r := range o.Down(context.Background()) {
					d.say("down %s: %s", r.Name, r.Status)
				}
			} else {
				d.say("exiting; services keep running")
			}
			return nil
		case <-tick.C:
			d.supervise(ctx)
		}
	}
}

func (d *Daemon) open() *orchestrator.Orchestrator {
	st, _ := state.Open(d.L.StateDir, d.L.Config.Project.Name)
	return &orchestrator.Orchestrator{L: d.L, S: st}
}

func (d *Daemon) supervise(ctx context.Context) {
	o := d.open()
	live := o.S.Reconcile()
	for name, svc := range d.L.Config.Services {
		e, ok := live[name]
		if !ok {
			continue
		}
		switch {
		case e.Status == state.Exited && svc.Restart == "on-failure":
			d.respawn(ctx, o, name, e)
		case e.Status == state.Running && svc.Healthcheck != nil:
			d.reprobe(ctx, o, name, e, svc)
		}
	}
}

func (d *Daemon) respawn(ctx context.Context, o *orchestrator.Orchestrator, name string, e state.ServiceState) {
	if e.Restarts >= orchestrator.MaxRestarts {
		return
	}
	if next, ok := d.nextRetry[name]; ok && time.Now().Before(next) {
		return
	}
	backoff := time.Second << e.Restarts
	if backoff > 30*time.Second {
		backoff = 30 * time.Second
	}
	d.nextRetry[name] = time.Now().Add(backoff)
	e.Restarts++
	o.S.Set(e)
	_ = o.S.Save()
	d.say("respawning %s (attempt %d)", name, e.Restarts)
	status := "unknown"
	for _, r := range o.Start(ctx, name) {
		if r.Name == name {
			status = string(r.Status)
			if r.Status == state.Running {
				if cur, ok := o.S.Get(name); ok {
					cur.Restarts = 0 // healthy again; next death restarts fresh
					o.S.Set(cur)
					_ = o.S.Save()
				}
			}
		}
	}
	d.say("respawn %s: %s", name, status)
}

func (d *Daemon) reprobe(ctx context.Context, o *orchestrator.Orchestrator, name string, e state.ServiceState, svc config.Service) {
	pctx, cancel := context.WithTimeout(ctx, svc.Healthcheck.Timeout.Std()+time.Second)
	defer cancel()
	if err := health.Wait(pctx, svc.Healthcheck, svc.Healthcheck.Timeout.Std()); err != nil {
		d.say("%s lost readiness (%v); restarting", name, err)
		r := o.Restart(ctx, name)
		d.say("restart %s: %s", name, r.Status)
	}
}
