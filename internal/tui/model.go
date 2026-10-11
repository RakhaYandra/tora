// Package tui provides the interactive dashboard (P1). It is the only
// package that imports Bubble Tea; plain commands never pay for it.
package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/RakhaYandra/tora/internal/config"
	"github.com/RakhaYandra/tora/internal/logs"
	"github.com/RakhaYandra/tora/internal/orchestrator"
	"github.com/RakhaYandra/tora/internal/state"
)

type tickMsg struct{}

// Model is the dashboard: service table + log pane of the selected service.
type Model struct {
	l       *config.Loaded
	names   []string
	live    map[string]state.ServiceState
	cursor  int
	logs    []string
	follow  bool
	filter  string
	editing bool
	msg     string
	w, h    int
}

// New builds the dashboard for a loaded project.
func New(l *config.Loaded) Model {
	names := make([]string, 0, len(l.Config.Services))
	for n := range l.Config.Services {
		names = append(names, n)
	}
	sort.Strings(names)
	m := Model{l: l, names: names, live: map[string]state.ServiceState{}, follow: true}
	m.refresh()
	return m
}

func (m *Model) refresh() {
	st, err := state.Open(m.l.StateDir, m.l.Config.Project.Name)
	if err != nil {
		m.msg = "state: " + err.Error()
		return
	}
	m.live = st.Reconcile()
	if m.cursor >= len(m.visible()) {
		m.cursor = 0
	}
	if m.follow {
		m.loadLogs()
	}
}

func (m *Model) visible() []string {
	if m.filter == "" {
		return m.names
	}
	var out []string
	for _, n := range m.names {
		if strings.Contains(n, m.filter) {
			out = append(out, n)
		}
	}
	return out
}

func (m *Model) selected() string {
	v := m.visible()
	if len(v) == 0 {
		return ""
	}
	return v[m.cursor]
}

func (m *Model) loadLogs() {
	name := m.selected()
	if name == "" {
		m.logs = nil
		return
	}
	lines, err := logs.Tail(logs.Path(m.l.LogDir, name), 100)
	if err != nil {
		m.msg = "logs: " + err.Error()
		return
	}
	m.logs = lines
}

// act runs a mutating orchestrator action under the project lock.
func (m *Model) act(what string, fn func(o *orchestrator.Orchestrator) string) {
	lock, err := state.Acquire(m.l.StateDir)
	if err != nil {
		m.msg = err.Error()
		return
	}
	defer lock.Release()
	st, err := state.Open(m.l.StateDir, m.l.Config.Project.Name)
	if err != nil {
		m.msg = err.Error()
		return
	}
	m.msg = what + ": " + fn(&orchestrator.Orchestrator{L: m.l, S: st})
	m.refresh()
}

func (m Model) Init() tea.Cmd { return tick() }

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, nil
	case tickMsg:
		m.refresh()
		return m, tick()
	case tea.KeyMsg:
		if m.editing {
			switch msg.String() {
			case "enter", "esc":
				m.editing = false
				m.cursor = 0
				m.loadLogs()
			case "backspace":
				if len(m.filter) > 0 {
					m.filter = m.filter[:len(m.filter)-1]
				}
			default:
				if len(msg.String()) == 1 {
					m.filter += msg.String()
				}
			}
			return m, nil
		}
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
				m.loadLogs()
			}
		case "down", "j":
			if m.cursor < len(m.visible())-1 {
				m.cursor++
				m.loadLogs()
			}
		case "f":
			m.follow = !m.follow
			m.loadLogs()
		case "/":
			m.editing = true
			m.filter = ""
		case "r":
			if n := m.selected(); n != "" {
				m.act("restart "+n, func(o *orchestrator.Orchestrator) string {
					r := o.Restart(context.Background(), n)
					if r.Status == state.Failed {
						return "FAILED: " + r.Error
					}
					return string(r.Status)
				})
			}
		case "s":
			if n := m.selected(); n != "" {
				if e, ok := m.live[n]; ok && e.Status == state.Running {
					m.act("stop "+n, func(o *orchestrator.Orchestrator) string {
						r := o.Stop(context.Background(), n)
						if r.Status == state.Failed {
							return "FAILED: " + r.Error
						}
						return string(r.Status)
					})
				} else {
					m.act("start "+n, func(o *orchestrator.Orchestrator) string {
						rs := o.Start(context.Background(), n)
						for _, r := range rs {
							if r.Name == n {
								if r.Status == state.Failed {
									return "FAILED: " + r.Error
								}
								return string(r.Status)
							}
						}
						return "done"
					})
				}
			}
		}
	}
	return m, nil
}

var (
	titleStyle = lipgloss.NewStyle().Bold(true)
	selStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	okStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	badStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	dimStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

func statusColor(s string) lipgloss.Style {
	switch s {
	case state.Running:
		return okStyle
	case state.Failed:
		return badStyle
	default:
		return dimStyle
	}
}

// View renders table + log pane + footer. Pure: safe to call in tests.
func (m Model) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("TORA  %s", m.l.Config.Project.Name)))
	b.WriteString("\n\n")
	for i, n := range m.visible() {
		st := state.Stopped
		pid := "-"
		if e, ok := m.live[n]; ok {
			st = e.Status
			if e.PID != 0 && (st == state.Running || st == state.Starting) {
				pid = fmt.Sprint(e.PID)
			}
		}
		mark := "  "
		if i == m.cursor {
			mark = "> "
		}
		line := fmt.Sprintf("%s%-12s %-10s %s", mark, n, st, pid)
		if i == m.cursor {
			line = selStyle.Render(line)
		} else {
			line = statusColor(st).Render(line)
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n")
	if n := m.selected(); n != "" {
		b.WriteString(dimStyle.Render(fmt.Sprintf("--- %s.log (follow:%v) ---", n, m.follow)) + "\n")
		lines := m.logs
		max := m.h/2 - 8
		if max < 5 {
			max = 5
		}
		if len(lines) > max {
			lines = lines[len(lines)-max:]
		}
		for _, l := range lines {
			b.WriteString(l + "\n")
		}
	}
	b.WriteString("\n")
	if m.editing {
		b.WriteString("filter: " + m.filter + "_   (enter/esc done)")
	} else {
		b.WriteString(dimStyle.Render("↑↓ select · s start/stop · r restart · f follow · / filter · q quit"))
	}
	if m.msg != "" {
		b.WriteString("\n" + m.msg)
	}
	return b.String()
}
