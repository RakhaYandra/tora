package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/RakhaYandra/tora/internal/orchestrator"
	"github.com/RakhaYandra/tora/internal/watch"
)

func newWatch() *cobra.Command {
	return &cobra.Command{
		Use:   "watch [SERVICE]",
		Short: "Restart services when files under their working dir change",
		Long: `Watch working directories (recursive, ignoring .git/node_modules/.tora/vendor/dist)
and restart affected services with a 500ms debounce. Blocks until interrupted;
exiting the watcher never stops services. A restart refused by the dependent
guard is reported and watching continues.`,
		Args: cobra.MaximumNArgs(1),
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
			w := &watch.Watcher{L: l, Target: target}
			if jsonOut {
				w.OnEvent = nil
				w.Event = func(r orchestrator.Result) {
					raw, _ := json.Marshal(r)
					fmt.Println(string(raw))
				}
			} else {
				w.OnEvent = func(s string) { fmt.Println("watch:", s) }
			}
			if !jsonOut {
				if target == "" {
					fmt.Printf("TORA  watching all services (Ctrl+C exits, services keep running)...\n")
				} else {
					fmt.Printf("TORA  watching %s (Ctrl+C exits, services keep running)...\n", target)
				}
			}
			// No project lock while idle: restarts acquire it per action,
			// so other tora commands stay usable during watch.
			if err := w.Run(ctx); err != nil {
				return fail(1, "%v", err)
			}
			if *fired {
				return fail(130, "watch interrupted")
			}
			return nil
		},
	}
}
