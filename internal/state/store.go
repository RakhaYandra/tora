package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Statuses for the service state machine.
const (
	Stopped  = "STOPPED"
	Starting = "STARTING"
	Running  = "RUNNING"
	Stopping = "STOPPING"
	Failed   = "FAILED"
	Exited   = "EXITED"
)

// ServiceState is the persisted lifecycle record of one service.
type ServiceState struct {
	Name      string    `json:"name"`
	PID       int       `json:"pid"`
	PGID      int       `json:"pgid"`
	StartTime string    `json:"start_time"` // kernel starttime tick, anti-PID-reuse
	Exe       string    `json:"exe"`
	Command   []string  `json:"command"`
	WorkDir   string    `json:"work_dir"`
	Status    string    `json:"status"`
	ExitCode  int       `json:"exit_code"`
	StartedAt time.Time `json:"started_at"`
	Restarts  int       `json:"restarts"`
	Error     string    `json:"error,omitempty"`
}

type fileFormat struct {
	Project  string                  `json:"project"`
	Services map[string]ServiceState `json:"services"`
}

// Store persists state.json atomically inside stateDir.
// Safe for concurrent use by parallel service startups.
type Store struct {
	mu      sync.Mutex
	path    string
	Project string
	entries map[string]ServiceState
}

// Open loads state.json; missing or corrupt files yield an empty store.
// A corrupt file is backed up to state.json.corrupt.<unix> rather than deleted.
func Open(stateDir, project string) (*Store, error) {
	s := &Store{path: filepath.Join(stateDir, "state.json"), Project: project, entries: map[string]ServiceState{}}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("read state: %w", err)
	}
	var f fileFormat
	if err := json.Unmarshal(raw, &f); err != nil {
		backup := s.path + fmt.Sprintf(".corrupt.%d", time.Now().Unix())
		_ = os.Rename(s.path, backup)
		return s, nil
	}
	if f.Services != nil {
		s.entries = f.Services
	}
	return s, nil
}

// Save writes atomically (temp file + rename) with mode 0600.
func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	f := fileFormat{Project: s.Project, Services: s.entries}
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), "state-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, s.path)
}

func (s *Store) Get(name string) (ServiceState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[name]
	return e, ok
}
func (s *Store) Set(e ServiceState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[e.Name] = e
}
func (s *Store) Delete(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, name)
}
func (s *Store) All() map[string]ServiceState {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]ServiceState, len(s.entries))
	for k, v := range s.entries {
		out[k] = v
	}
	return out
}

// Reconcile verifies every entry against live processes. Stale entries are
// dropped without touching any foreign process. It returns the live view.
func (s *Store) Reconcile() map[string]ServiceState {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for name, e := range s.entries {
		switch e.Status {
		case Running, Starting, Stopping:
			if e.PID <= 0 || !Owns(e.PID, e.PGID, e.StartTime) {
				if Inspect(e.PID).Alive {
					// PID alive but not ours (reused): drop, never signal.
					delete(s.entries, name)
				} else {
					e.Status = Exited
					e.ExitCode = -1 // not our child; kernel exit code unknowable
					e.Error = "process exited (exit code unavailable: not supervised in this invocation)"
					s.entries[name] = e
				}
				changed = true
			}
		}
	}
	if changed {
		_ = s.saveLocked() // best effort; callers re-save after mutations anyway
	}
	out := make(map[string]ServiceState, len(s.entries))
	for k, v := range s.entries {
		out[k] = v
	}
	return out
}
