package daemon

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
	"github.com/RakhaYandra/tora/internal/state"
)

func testLoaded(t *testing.T) *config.Loaded {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "tora.yaml")
	body := "version: 1\nproject:\n  name: daemontest\nruntime: {}\nservices:\n  app:\n    command: [\"sleep\", \"30\"]\n    restart: \"on-failure\"\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	l, err := config.Resolve(cfg, dir)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func freshDown(t *testing.T, l *config.Loaded) {
	t.Helper()
	st, _ := state.Open(l.StateDir, "daemontest")
	(&orchestrator.Orchestrator{L: l, S: st}).Down(context.Background())
}

func TestRespawnDeadService(t *testing.T) {
	l := testLoaded(t)
	st, _ := state.Open(l.StateDir, "daemontest")
	o := &orchestrator.Orchestrator{L: l, S: st}
	if r := o.Start(context.Background(), "app"); r[0].Status != state.Running {
		t.Fatalf("start: %+v", r[0])
	}
	t.Cleanup(func() { freshDown(t, l) })
	old, _ := state.Open(l.StateDir, "daemontest")
	oldPID := old.All()["app"].PID

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := &Daemon{L: l, Interval: 300 * time.Millisecond}
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()

	// Externally kill the service; daemon must respawn it.
	time.Sleep(500 * time.Millisecond)
	syscall.Kill(oldPID, syscall.SIGTERM)
	deadline := time.Now().Add(12 * time.Second)
	for {
		cur, _ := state.Open(l.StateDir, "daemontest")
		if e := cur.All()["app"]; e.Status == state.Running && e.PID != oldPID {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon did not respawn the killed service")
		}
		time.Sleep(200 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("daemon run: %v", err)
	}
	if state.Inspect(oldPID).Alive {
		t.Fatal("original pid must stay dead")
	}
}

func TestServicesSurviveDaemonExit(t *testing.T) {
	l := testLoaded(t)
	st, _ := state.Open(l.StateDir, "daemontest")
	o := &orchestrator.Orchestrator{L: l, S: st}
	o.Start(context.Background(), "app")
	t.Cleanup(func() { freshDown(t, l) })

	var events []string
	ctx, cancel := context.WithCancel(context.Background())
	d := &Daemon{L: l, Interval: 300 * time.Millisecond, OnEvent: func(s string) {
		events = append(events, s)
	}}
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	time.Sleep(700 * time.Millisecond)
	cancel()
	<-done
	cur, _ := state.Open(l.StateDir, "daemontest")
	if e := cur.All()["app"]; e.Status != state.Running {
		t.Fatalf("service must keep running after daemon exit: %+v", e)
	}
	joined := strings.Join(events, "\n")
	if !strings.Contains(joined, "keep running") {
		t.Fatalf("exit message must say services keep running:\n%s", joined)
	}
	if _, err := os.Stat(filepath.Join(l.StateDir, "daemon.log")); err != nil {
		t.Fatal("daemon must append daemon.log")
	}
}

func TestDownOnExit(t *testing.T) {
	l := testLoaded(t)
	st, _ := state.Open(l.StateDir, "daemontest")
	o := &orchestrator.Orchestrator{L: l, S: st}
	o.Start(context.Background(), "app")
	t.Cleanup(func() { freshDown(t, l) })

	ctx, cancel := context.WithCancel(context.Background())
	d := &Daemon{L: l, Interval: time.Hour, DownOnExit: true}
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	time.Sleep(300 * time.Millisecond)
	cancel()
	<-done
	cur, _ := state.Open(l.StateDir, "daemontest")
	if e := cur.All()["app"]; e.Status != state.Stopped {
		t.Fatalf("down-on-exit must stop services: %+v", e)
	}
}
