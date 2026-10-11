package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/RakhaYandra/tora/internal/config"
	"github.com/RakhaYandra/tora/internal/state"
)

var (
	// version is set at build time: -X github.com/RakhaYandra/tora/internal/cli.version
	version    = "dev"
	cfgPath    string
	projectDir string
	noColor    bool
	jsonOut    bool
	verbose    bool
)

// ExitError carries a process exit code across cobra.
type ExitError struct {
	Code int
	Msg  string
}

func (e *ExitError) Error() string { return e.Msg }

func fail(code int, format string, args ...any) error {
	return &ExitError{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// signalCtx cancels on SIGINT/SIGTERM and reports whether a signal arrived.
func signalCtx() (context.Context, *bool) {
	ctx, cancel := context.WithCancel(context.Background())
	fired := new(bool)
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-ch
		*fired = true
		cancel()
		<-ch
		cancel()
	}()
	return ctx, fired
}

func debugf(format string, args ...any) {
	if verbose {
		fmt.Fprintf(os.Stderr, "tora: "+format+"\n", args...)
	}
}

// loadProject finds, loads, resolves config and opens state. Read-only except
// directory creation for log/state dirs (documented side effect of commands
// that manage processes; `config validate` avoids it).
func loadProject() (*config.Loaded, *state.Store, error) {
	path := cfgPath
	if path == "" {
		cwd := projectDir
		if cwd == "" {
			var err error
			cwd, err = os.Getwd()
			if err != nil {
				return nil, nil, fail(1, "get working directory: %v", err)
			}
		}
		path = config.Find(cwd)
		if path == "" {
			return nil, nil, fail(2, "no tora.yaml found (run `tora init` first)")
		}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, nil, fail(2, "bad --config path: %v", err)
	}
	cfg, err := config.Load(abs)
	if err != nil {
		return nil, nil, fail(2, "%v", err)
	}
	dir := projectDir
	if dir == "" {
		dir = filepath.Dir(abs)
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return nil, nil, fail(2, "bad --project-dir: %v", err)
	}
	l, err := config.Resolve(cfg, dir)
	if err != nil {
		return nil, nil, fail(2, "%v", err)
	}
	l.Path = abs
	st, err := state.Open(l.StateDir, cfg.Project.Name)
	if err != nil {
		return nil, nil, fail(1, "%v", err)
	}
	debugf("config %s project %s", abs, cfg.Project.Name)
	return l, st, nil
}

// New builds the root command tree.
func New() *cobra.Command {
	root := &cobra.Command{
		Use:   "tora",
		Short: "Developer process orchestrator: one project, one command, full control",
		Long: `Tora runs and supervises your local development processes (api, worker,
web, dependencies) from a single tora.yaml: start order, logs, status,
restart, and coordinated shutdown.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVarP(&cfgPath, "config", "c", "", "path to tora.yaml")
	root.PersistentFlags().StringVar(&projectDir, "project-dir", "", "project root (default: config directory)")
	root.PersistentFlags().BoolVar(&noColor, "no-color", false, "disable colored output")
	root.PersistentFlags().BoolVar(&jsonOut, "json", false, "structured JSON output")
	root.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "diagnostic output on stderr")

	root.AddCommand(newInit(), newUp(), newDown(), newPs(),
		newStart(), newStop(), newRestart(), newLogs(), newConfig(), newVersion(), newTui(), newWatch())
	return root
}
