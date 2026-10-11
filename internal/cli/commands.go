package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/RakhaYandra/tora/internal/config"
	"github.com/RakhaYandra/tora/internal/orchestrator"
	"github.com/RakhaYandra/tora/internal/state"
)

const initTemplate = `version: 1

project:
  name: %s

runtime:
  startup_timeout: 30s
  shutdown_timeout: 10s
  log_dir: .tora/logs
  state_dir: .tora

services:
  api:
    command: ["go", "run", "./cmd/api"]
    working_dir: .
    env:
      APP_ENV: development
      PORT: "8080"
    restart: "no"

  worker:
    command: ["go", "run", "./cmd/worker"]
    working_dir: .
    depends_on:
      - api
    env:
      APP_ENV: development
    restart: "no"
`

func newInit() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init [--name NAME]",
		Short: "Create tora.yaml in the project directory",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir := projectDir
			if dir == "" {
				var err error
				dir, err = os.Getwd()
				if err != nil {
					return fail(1, "%v", err)
				}
			}
			name, _ := cmd.Flags().GetString("name")
			if name == "" {
				name = filepath.Base(dir)
			}
			path := filepath.Join(dir, "tora.yaml")
			if _, err := os.Stat(path); err == nil && !force {
				return fail(1, "%s already exists (use --force to overwrite)", path)
			}
			if err := os.WriteFile(path, []byte(fmt.Sprintf(initTemplate, name)), 0o644); err != nil {
				return fail(1, "write %s: %v", path, err)
			}
			ensureGitignore(dir)
			fmt.Printf("created %s\n", path)
			return nil
		},
	}
	cmd.Flags().String("name", "", "project name (default: directory name)")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite existing tora.yaml")
	return cmd
}

func ensureGitignore(dir string) {
	p := filepath.Join(dir, ".gitignore")
	raw, _ := os.ReadFile(p)
	if strings.Contains(string(raw), ".tora/") {
		return
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	if len(raw) > 0 && !strings.HasSuffix(string(raw), "\n") {
		fmt.Fprintln(f)
	}
	fmt.Fprintln(f, ".tora/")
}

func withLock(l *config.Loaded, fn func(*orchestrator.Orchestrator, *state.Store) error) error {
	lock, err := state.Acquire(l.StateDir)
	if err != nil {
		return fail(1, "%v", err)
	}
	defer lock.Release()
	st, err := state.Open(l.StateDir, l.Config.Project.Name)
	if err != nil {
		return fail(1, "%v", err)
	}
	return fn(&orchestrator.Orchestrator{L: l, S: st}, st)
}

func emitResults(results []orchestrator.Result, verb string) {
	if jsonOut {
		raw, _ := json.MarshalIndent(results, "", "  ")
		fmt.Println(string(raw))
		return
	}
	ok := 0
	for _, r := range results {
		mark := "ok"
		if r.Status == "FAILED" {
			mark = "FAIL"
		} else if r.Error != "" && r.Status == "STOPPED" {
			mark = "skip"
		}
		if r.Status == "RUNNING" || r.Status == "EXITED" || r.Status == "STOPPED" {
			if r.Error == "" || r.Status != "STOPPED" {
				ok++
			}
		}
		line := fmt.Sprintf("  %-3s %-12s %s", mark, r.Name, strings.ToLower(r.Status))
		if r.PID != 0 {
			line += fmt.Sprintf("  pid %d", r.PID)
		}
		if r.Error != "" {
			line += fmt.Sprintf("  (%s)", r.Error)
		}
		fmt.Println(line)
	}
	_ = ok
	_ = verb
}

func opFailed(results []orchestrator.Result) bool {
	for _, r := range results {
		if r.Status == "FAILED" {
			return true
		}
	}
	return false
}

func newUp() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "up [SERVICE]",
		Short: "Start all services (or SERVICE plus its dependencies)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			l, _, err := loadProject()
			if err != nil {
				return err
			}
			target := ""
			if len(args) == 1 {
				target = args[0]
				if _, ok := l.Config.Services[target]; !ok {
					return fail(2, "unknown service %q", target)
				}
			}
			ctx, fired := signalCtx()
			var results []orchestrator.Result
			if err := withLock(l, func(o *orchestrator.Orchestrator, _ *state.Store) error {
				if !jsonOut {
					fmt.Printf("TORA  project %s: starting services...\n", l.Config.Project.Name)
				}
				results = o.Up(ctx, target)
				return nil
			}); err != nil {
				return err
			}
			emitResults(results, "started")
			switch {
			case *fired:
				return fail(130, "cancelled by signal")
			case opFailed(results):
				return fail(1, "startup failed")
			}
			return nil
		},
	}
	return cmd
}

func newDown() *cobra.Command {
	return &cobra.Command{
		Use:   "down",
		Short: "Stop all managed services in reverse dependency order",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			l, _, err := loadProject()
			if err != nil {
				return err
			}
			ctx, fired := signalCtx()
			var results []orchestrator.Result
			if err := withLock(l, func(o *orchestrator.Orchestrator, _ *state.Store) error {
				if !jsonOut {
					fmt.Printf("TORA  project %s: stopping services...\n", l.Config.Project.Name)
				}
				results = o.Down(ctx)
				return nil
			}); err != nil {
				return err
			}
			emitResults(results, "stopped")
			switch {
			case *fired:
				return fail(130, "cancelled by signal")
			case opFailed(results):
				return fail(1, "shutdown incomplete")
			}
			return nil
		},
	}
}

type psEntry struct {
	Name      string `json:"name"`
	Status    string `json:"status"`
	PID       int    `json:"pid,omitempty"`
	ExitCode  int    `json:"exit_code,omitempty"`
	StartedAt string `json:"started_at,omitempty"`
	Error     string `json:"error,omitempty"`
}

func newPs() *cobra.Command {
	return &cobra.Command{
		Use:   "ps",
		Short: "Show live status of managed services",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			l, st, err := loadProject()
			if err != nil {
				return err
			}
			live := st.Reconcile()
			names := make([]string, 0, len(l.Config.Services))
			for n := range l.Config.Services {
				names = append(names, n)
			}
			// deterministic order
			for i := 0; i < len(names); i++ {
				for j := i + 1; j < len(names); j++ {
					if names[j] < names[i] {
						names[i], names[j] = names[j], names[i]
					}
				}
			}
			if jsonOut {
				entries := make([]psEntry, 0, len(names))
				for _, n := range names {
					e := psEntry{Name: n, Status: state.Stopped}
					if s, ok := live[n]; ok {
						e = psEntry{Name: n, Status: s.Status, PID: s.PID,
							ExitCode: s.ExitCode, StartedAt: s.StartedAt.Format(time.RFC3339), Error: s.Error}
					}
					entries = append(entries, e)
				}
				raw, _ := json.MarshalIndent(map[string]any{"project": l.Config.Project.Name, "services": entries}, "", "  ")
				fmt.Println(string(raw))
				return nil
			}
			fmt.Printf("%-12s %-10s %-8s %s\n", "SERVICE", "STATUS", "PID", "STARTED")
			for _, n := range names {
				status, pid, started := state.Stopped, "-", "-"
				if s, ok := live[n]; ok {
					status = s.Status
					if s.PID != 0 && (s.Status == state.Running || s.Status == state.Starting || s.Status == state.Stopping) {
						pid = fmt.Sprint(s.PID)
					}
					if !s.StartedAt.IsZero() {
						started = s.StartedAt.Format("15:04:05")
					}
				}
				fmt.Printf("%-12s %-10s %-8s %s\n", n, status, pid, started)
			}
			return nil
		},
	}
}

func newStart() *cobra.Command {
	return &cobra.Command{
		Use:   "start SERVICE",
		Short: "Start a service plus any dependencies not already running",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			l, _, err := loadProject()
			if err != nil {
				return err
			}
			if _, ok := l.Config.Services[args[0]]; !ok {
				return fail(2, "unknown service %q", args[0])
			}
			ctx, fired := signalCtx()
			var results []orchestrator.Result
			if err := withLock(l, func(o *orchestrator.Orchestrator, _ *state.Store) error {
				results = o.Start(ctx, args[0])
				return nil
			}); err != nil {
				return err
			}
			emitResults(results, "started")
			switch {
			case *fired:
				return fail(130, "cancelled by signal")
			case opFailed(results):
				return fail(1, "start failed")
			}
			return nil
		},
	}
}

func newStop() *cobra.Command {
	return &cobra.Command{
		Use:   "stop SERVICE",
		Short: "Stop a service (refused while a dependent is running)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			l, _, err := loadProject()
			if err != nil {
				return err
			}
			if _, ok := l.Config.Services[args[0]]; !ok {
				return fail(2, "unknown service %q", args[0])
			}
			ctx, fired := signalCtx()
			var r orchestrator.Result
			if err := withLock(l, func(o *orchestrator.Orchestrator, _ *state.Store) error {
				r = o.Stop(ctx, args[0])
				return nil
			}); err != nil {
				return err
			}
			emitResults([]orchestrator.Result{r}, "stopped")
			switch {
			case *fired:
				return fail(130, "cancelled by signal")
			case r.Status == "FAILED":
				return fail(1, "stop failed: %s", r.Error)
			}
			return nil
		},
	}
}

func newRestart() *cobra.Command {
	return &cobra.Command{
		Use:   "restart SERVICE",
		Short: "Gracefully restart a service (dependents must be stopped first)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			l, _, err := loadProject()
			if err != nil {
				return err
			}
			if _, ok := l.Config.Services[args[0]]; !ok {
				return fail(2, "unknown service %q", args[0])
			}
			ctx, fired := signalCtx()
			var r orchestrator.Result
			if err := withLock(l, func(o *orchestrator.Orchestrator, _ *state.Store) error {
				r = o.Restart(ctx, args[0])
				return nil
			}); err != nil {
				return err
			}
			emitResults([]orchestrator.Result{r}, "restarted")
			switch {
			case *fired:
				return fail(130, "cancelled by signal")
			case r.Status == "FAILED":
				return fail(1, "restart failed: %s", r.Error)
			}
			return nil
		},
	}
}

func newVersion() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print tora version",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if jsonOut {
				fmt.Printf("{\"version\":%q}\n", version)
				return nil
			}
			fmt.Printf("tora %s\n", version)
			return nil
		},
	}
}
