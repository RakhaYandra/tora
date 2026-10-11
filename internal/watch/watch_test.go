package watch

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RakhaYandra/tora/internal/config"
	"github.com/RakhaYandra/tora/internal/orchestrator"
	"github.com/RakhaYandra/tora/internal/state"
)

func testLoaded(t *testing.T, workdir string) *config.Loaded {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "tora.yaml")
	body := "version: 1\nproject:\n  name: watchtest\nruntime: {}\nservices:\n  app:\n    command: [\"sleep\", \"30\"]\n    working_dir: " + workdir + "\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(workdir, 0o755)
	l, err := config.Resolve(cfg, dir)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func startSvc(t *testing.T, l *config.Loaded) {
	t.Helper()
	st, _ := state.Open(l.StateDir, "watchtest")
	o := &orchestrator.Orchestrator{L: l, S: st}
	if r := o.Start(context.Background(), "app"); r[0].Status != state.Running {
		t.Fatalf("start: %+v", r[0])
	}
	// Fresh open like production commands: the watcher saves via its own
	// store, so this instance would otherwise miss restarted PIDs on Down.
	t.Cleanup(func() {
		st2, _ := state.Open(l.StateDir, "watchtest")
		(&orchestrator.Orchestrator{L: l, S: st2}).Down(context.Background())
	})
}

func TestRestartOnChange(t *testing.T) {
	work := t.TempDir()
	l := testLoaded(t, work)
	startSvc(t, l)
	before, _ := state.Open(l.StateDir, "watchtest")
	oldPID := before.All()["app"].PID

	events := make(chan string, 16)
	w := &Watcher{L: l, OnEvent: func(s string) { events <- s }}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)
	time.Sleep(300 * time.Millisecond) // let watcher arm
	if err := os.WriteFile(filepath.Join(work, "main.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		st, _ := state.Open(l.StateDir, "watchtest")
		if e := st.All()["app"]; e.PID != 0 && e.PID != oldPID && e.Status == state.Running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("service was not restarted after file change")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func TestDebounceAndIgnore(t *testing.T) {
	work := t.TempDir()
	l := testLoaded(t, work)
	startSvc(t, l)
	count := atomic.Int32{}
	w := &Watcher{L: l, Event: func(r orchestrator.Result) { count.Add(1) }}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)
	time.Sleep(300 * time.Millisecond)
	// Burst of writes must coalesce into one restart.
	for i := 0; i < 5; i++ {
		os.WriteFile(filepath.Join(work, "a.txt"), []byte{byte(i)}, 0o644)
		time.Sleep(50 * time.Millisecond)
	}
	// Ignored subtree must not restart anything.
	ig := filepath.Join(work, "node_modules", "lib")
	os.MkdirAll(ig, 0o755)
	time.Sleep(300 * time.Millisecond) // let watcher pick up the new dir
	os.WriteFile(filepath.Join(ig, "x.js"), []byte("x"), 0o644)
	time.Sleep(2 * time.Second)
	if count.Load() != 1 {
		t.Fatalf("restarts = %d, want 1 (debounced burst, ignored dir silent)", count.Load())
	}
}
