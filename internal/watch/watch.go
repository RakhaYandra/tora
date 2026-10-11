// Package watch restarts services when files under their working directory
// change. It never holds the project lock while idle; each restart acquires
// it like any other command. Exiting the watcher never stops services.
package watch

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/RakhaYandra/tora/internal/config"
	"github.com/RakhaYandra/tora/internal/orchestrator"
	"github.com/RakhaYandra/tora/internal/state"
)

// Debounce coalesces editor save bursts into one restart.
const Debounce = 500 * time.Millisecond

// Ignored directory names (any path component match skips the subtree).
// .tora is critical: service log writes must never trigger restarts.
var Ignored = []string{".git", "node_modules", ".tora", "vendor", "dist", "target", "__pycache__"}

func ignored(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		for _, ig := range Ignored {
			if part == ig {
				return true
			}
		}
	}
	return false
}

// Watcher monitors working directories and restarts affected services.
type Watcher struct {
	L      *config.Loaded
	Target string // "" = all services
	// OnEvent reports human-readable activity; nil silences (tests use Event).
	OnEvent func(string)
	// Event receives structured restart outcomes; nil disables.
	Event func(orchestrator.Result)

	wg sync.WaitGroup // in-flight restarts; Run waits for them on shutdown
}

func (w *Watcher) say(format string, args ...any) {
	if w.OnEvent != nil {
		w.OnEvent(fmt.Sprintf(format, args...))
	}
}

// owners returns services whose working dir contains path (or all when target
// is set and contains it).
func (w *Watcher) owners(path string) []string {
	var out []string
	for name, dir := range w.L.WorkDirs {
		if w.Target != "" && name != w.Target {
			continue
		}
		rel, err := filepath.Rel(dir, path)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			out = append(out, name)
		}
	}
	return out
}

func addRecursive(fw *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // transient; keep watching the rest
		}
		if d.IsDir() {
			if ignored(p) {
				return filepath.SkipDir
			}
			_ = fw.Add(p) // already-watched is harmless
		}
		return nil
	})
}

// Run blocks until ctx ends, restarting debounced services on file changes.
func (w *Watcher) Run(ctx context.Context) error {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer fw.Close()
	dirs := map[string]bool{}
	for name, dir := range w.L.WorkDirs {
		if w.Target != "" && name != w.Target {
			continue
		}
		if !dirs[dir] {
			dirs[dir] = true
			if err := addRecursive(fw, dir); err != nil {
				return err
			}
		}
	}
	timers := map[string]*time.Timer{}
	arm := func(name string) {
		if t, ok := timers[name]; ok {
			if t.Stop() {
				w.wg.Done() // cancelled pre-fire; balance its Add
			}
			// Stop()==false: callback owns the Done (started or finished).
		}
		w.wg.Add(1)
		timers[name] = time.AfterFunc(Debounce, func() {
			defer w.wg.Done()
			w.restart(name)
		})
	}
	for {
		select {
		case <-ctx.Done():
			for _, t := range timers {
				t.Stop()
			}
			w.wg.Wait() // never strand a restart mid-grace on shutdown
			return nil
		case err := <-fw.Errors:
			w.say("watch error: %v", err)
		case ev := <-fw.Events:
			if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) == 0 {
				continue
			}
			if ignored(ev.Name) {
				continue
			}
			if ev.Op&(fsnotify.Create|fsnotify.Rename) != 0 {
				if st, err := os.Stat(ev.Name); err == nil && st.IsDir() {
					_ = addRecursive(fw, ev.Name)
				}
			}
			for _, name := range w.owners(ev.Name) {
				w.say("change detected: %s -> restart %s", ev.Name, name)
				arm(name)
			}
		}
	}
}

func (w *Watcher) restart(name string) {
	lock, err := state.Acquire(w.L.StateDir)
	if err != nil {
		w.say("restart %s skipped: %v", name, err)
		return
	}
	defer lock.Release()
	st, err := state.Open(w.L.StateDir, w.L.Config.Project.Name)
	if err != nil {
		w.say("restart %s skipped: %v", name, err)
		return
	}
	o := &orchestrator.Orchestrator{L: w.L, S: st}
	r := o.Restart(context.Background(), name)
	if r.Status == state.Failed {
		w.say("restart %s FAILED: %s", name, r.Error)
	} else {
		w.say("restart %s: %s pid %d", name, r.Status, r.PID)
	}
	if w.Event != nil {
		w.Event(r)
	}
}
