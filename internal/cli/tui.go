package cli

import (
	"github.com/spf13/cobra"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/RakhaYandra/tora/internal/tui"
)

func newTui() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Interactive dashboard: live status, logs, restart",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			l, _, err := loadProject()
			if err != nil {
				return err
			}
			p := tea.NewProgram(tui.New(l), tea.WithAltScreen())
			if _, err := p.Run(); err != nil {
				return fail(1, "tui: %v", err)
			}
			return nil
		},
	}
}
