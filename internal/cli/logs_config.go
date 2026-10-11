package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	"github.com/RakhaYandra/tora/internal/config"
	"github.com/RakhaYandra/tora/internal/logs"
)

func newLogs() *cobra.Command {
	var follow bool
	var tailN int
	var all bool
	cmd := &cobra.Command{
		Use:   "logs [SERVICE] [--follow] [--tail N] [--all]",
		Short: "Read service logs (stdout+stderr capture)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			l, _, err := loadProject()
			if err != nil {
				return err
			}
			var names []string
			switch {
			case all:
				for n := range l.Config.Services {
					names = append(names, n)
				}
				sort.Strings(names)
			case len(args) == 1:
				if _, ok := l.Config.Services[args[0]]; !ok {
					return fail(2, "unknown service %q", args[0])
				}
				names = []string{args[0]}
			default:
				return fail(2, "specify a SERVICE or --all")
			}
			if follow {
				ctx, stop := context.WithCancel(context.Background())
				// Follow ends on EOF of stdin? No: follow runs until signal.
				// Use signalCtx so Ctrl+C exits 130.
				sctx, _ := signalCtx()
				_ = stop
				_ = ctx
				if err := logs.Follow(sctx, l.LogDir, names, tailN, all, os.Stdout); err != nil {
					return fail(1, "%v", err)
				}
				return nil
			}
			for _, n := range names {
				lines, err := logs.Tail(logs.Path(l.LogDir, n), tailN)
				if err != nil {
					return fail(1, "%v", err)
				}
				for _, line := range lines {
					if all {
						fmt.Printf("[%s] %s\n", n, line)
					} else {
						fmt.Println(line)
					}
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "stream new log lines until interrupted")
	cmd.Flags().IntVar(&tailN, "tail", 0, "show only the last N lines (0 = all)")
	cmd.Flags().BoolVar(&all, "all", false, "read all services with [service] prefix")
	return cmd
}

func newConfig() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect and validate configuration",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "validate",
		Short: "Validate tora.yaml without spawning any process",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			path := cfgPath
			if path == "" {
				cwd := projectDir
				if cwd == "" {
					var err error
					cwd, err = os.Getwd()
					if err != nil {
						return fail(1, "%v", err)
					}
				}
				path = config.Find(cwd)
				if path == "" {
					return fail(2, "no tora.yaml found")
				}
			}
			cfg, err := config.Load(path)
			if err != nil {
				// Load already validates; surface as exit 2.
				return fail(2, "%v", err)
			}
			dir := projectDir
			if dir == "" {
				dir = filepath.Dir(path)
			}
			for name, s := range cfg.Services {
				wd := s.WorkingDir
				if wd == "" {
					wd = "."
				}
				if !filepath.IsAbs(wd) {
					wd = filepath.Join(dir, wd)
				}
				if st, err := os.Stat(wd); err != nil || !st.IsDir() {
					return fail(2, "services.%s.working_dir: directory unavailable: %s", name, wd)
				}
			}
			fmt.Println("configuration valid")
			return nil
		},
	})
	return cmd
}
