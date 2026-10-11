package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenMissing(t *testing.T) {
	s, err := Open(t.TempDir(), "pulse")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.All()) != 0 {
		t.Fatal("expected empty store")
	}
}

func TestSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir, "pulse")
	s.Set(ServiceState{Name: "api", PID: 1234, Status: Running, StartedAt: time.Now()})
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(filepath.Join(dir, "state.json"))
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("state mode = %o, want 600", st.Mode().Perm())
	}
	s2, _ := Open(dir, "pulse")
	e, ok := s2.Get("api")
	if !ok || e.PID != 1234 {
		t.Fatalf("round trip failed: %+v %v", e, ok)
	}
}

func TestCorruptBackedUp(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "state.json"), []byte("{nope"), 0o600)
	s, err := Open(dir, "pulse")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.All()) != 0 {
		t.Fatal("expected empty store after corrupt file")
	}
	m, _ := filepath.Glob(filepath.Join(dir, "state.json.corrupt.*"))
	if len(m) != 1 {
		t.Fatalf("expected 1 backup, got %v", m)
	}
}

func TestReconcileStale(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir, "pulse")
	// PID that surely does not exist.
	s.Set(ServiceState{Name: "api", PID: 1 << 30, PGID: 1 << 30, StartTime: "999", Status: Running})
	live := s.Reconcile()
	e := live["api"]
	if e.Status != Exited {
		t.Fatalf("stale entry status = %q, want EXITED", e.Status)
	}
}

func TestReconcileSelf(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir, "pulse")
	me := os.Getpid()
	info := Inspect(me)
	s.Set(ServiceState{Name: "self", PID: me, PGID: info.PGID, StartTime: info.StartTime, Status: Running})
	live := s.Reconcile()
	if live["self"].Status != Running {
		t.Fatalf("own process must stay RUNNING: %+v", live["self"])
	}
}

func TestLockExclusive(t *testing.T) {
	dir := t.TempDir()
	l1, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l1.Release()
	if _, err := Acquire(dir); err == nil {
		t.Fatal("second acquire must fail")
	}
}
