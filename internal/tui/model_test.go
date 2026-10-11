package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/RakhaYandra/tora/internal/config"
	"github.com/RakhaYandra/tora/internal/state"
)

func testLoaded(t *testing.T) *config.Loaded {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "tora.yaml")
	body := "version: 1\nproject:\n  name: tuitest\nruntime: {}\nservices:\n  api:\n    command: [\"sleep\", \"30\"]\n  worker:\n    command: [\"sleep\", \"30\"]\n"
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

func key(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestViewListsServices(t *testing.T) {
	m := New(testLoaded(t))
	v := m.View()
	if !strings.Contains(v, "api") || !strings.Contains(v, "worker") {
		t.Fatalf("view must list services:\n%s", v)
	}
}

func TestCursorMovement(t *testing.T) {
	m := New(testLoaded(t))
	updated, _ := m.Update(key("j"))
	m = updated.(Model)
	if m.cursor != 1 {
		t.Fatalf("cursor = %d, want 1", m.cursor)
	}
	updated, _ = m.Update(key("j"))
	m = updated.(Model)
	if m.cursor != 1 {
		t.Fatalf("cursor must clamp at end, got %d", m.cursor)
	}
	updated, _ = m.Update(key("k"))
	m = updated.(Model)
	if m.cursor != 0 {
		t.Fatalf("cursor = %d, want 0", m.cursor)
	}
}

func TestFilterNarrows(t *testing.T) {
	m := New(testLoaded(t))
	updated, _ := m.Update(key("/"))
	m = updated.(Model)
	for _, r := range "work" {
		updated, _ = m.Update(key(string(r)))
		m = updated.(Model)
	}
	if got := m.visible(); len(got) != 1 || got[0] != "worker" {
		t.Fatalf("visible = %v, want [worker]", got)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.editing {
		t.Fatal("enter must leave filter mode")
	}
}

func TestQuit(t *testing.T) {
	m := New(testLoaded(t))
	_, cmd := m.Update(key("q"))
	if cmd == nil {
		t.Fatal("q must return a command")
	}
	if msg := cmd(); msg != (tea.QuitMsg{}) {
		t.Fatalf("q must quit, got %#v", msg)
	}
}

func TestRefreshSeesLiveSelf(t *testing.T) {
	l := testLoaded(t)
	me := os.Getpid()
	info := state.Inspect(me)
	st, _ := state.Open(l.StateDir, "tuitest")
	st.Set(state.ServiceState{Name: "api", PID: me, PGID: info.PGID,
		StartTime: info.StartTime, Status: state.Running})
	_ = st.Save()
	m := New(l)
	if e, ok := m.live["api"]; !ok || e.Status != state.Running {
		t.Fatalf("refresh must see live self entry: %+v", m.live["api"])
	}
}

func TestFollowToggle(t *testing.T) {
	m := New(testLoaded(t))
	if !m.follow {
		t.Fatal("follow defaults on")
	}
	updated, _ := m.Update(key("f"))
	m = updated.(Model)
	if m.follow {
		t.Fatal("f must toggle follow off")
	}
}
