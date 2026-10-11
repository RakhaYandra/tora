package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/RakhaYandra/tora/internal/daemon"
)

func newDaemon() *cobra.Command {
	var interval time.Duration
	var downOnExit bool
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Supervise continuously in the foreground (holds the project lock)",
		Long: `Run continuous supervision: stale reconciliation, on-failure respawns
with backoff, and health re-probes. Holds the project lock, so other
mutating commands fail fast while it runs (ps/logs keep working).
Ctrl+C exits (code 130); services keep running unless --down-on-exit.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			l, _, err := loadProject()
			if err != nil {
				return err
			}
			ctx, fired := signalCtx()
			d := &daemon.Daemon{L: l, Interval: interval, DownOnExit: downOnExit}
			if jsonOut {
				d.OnEvent = nil
			} else {
				d.OnEvent = func(s string) { fmt.Println("daemon:", s) }
			}
			// Acquire inside Run: lock held for the daemon lifetime.
			if err := d.Run(ctx); err != nil {
				return fail(1, "%v", err)
			}
			if *fired {
				return fail(130, "daemon interrupted")
			}
			return nil
		},
	}
	cmd.Flags().DurationVar(&interval, "interval", 5*time.Second, "supervision tick interval")
	cmd.Flags().BoolVar(&downOnExit, "down-on-exit", false, "stop all services when the daemon exits")
	return cmd
}
