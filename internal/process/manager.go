// Package process spawns and signals service processes on Linux.
// Every service runs in its own process group so signals reach the main
// process and its children, and only groups verified by identity are touched.
package process

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/RakhaYandra/tora/internal/state"
)

// Child describes a spawned service process.
type Child struct {
	Cmd       *exec.Cmd
	PID       int
	PGID      int
	StartTime string
	Exe       string
}

// Spawn starts argv in workDir with envOverride applied over os.Environ.
// stdout+stderr append to logPath. No shell is involved. The child leads a
// new process group (PGID == child PID).
func Spawn(argv []string, workDir string, envOverride map[string]string, logPath string) (*Child, error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("empty command")
	}
	exe, err := exec.LookPath(argv[0])
	if err != nil {
		return nil, fmt.Errorf("command %q not found: %w", argv[0], err)
	}
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}
	defer log.Close()

	cmd := exec.Command(exe, argv[1:]...)
	cmd.Dir = workDir
	cmd.Env = os.Environ()
	for k, v := range envOverride {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	null, err := os.OpenFile(os.DevNull, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer null.Close()
	cmd.Stdin = null
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %q: %w", argv[0], err)
	}
	info := state.Inspect(cmd.Process.Pid)
	if !info.Alive {
		return nil, fmt.Errorf("process exited immediately after spawn")
	}
	return &Child{Cmd: cmd, PID: cmd.Process.Pid, PGID: cmd.Process.Pid, StartTime: info.StartTime, Exe: exe}, nil
}

// SignalGroup sends sig to -pgid after verifying ownership. It never touches
// processes that fail identity verification.
func SignalGroup(pid, pgid int, startTime string, sig syscall.Signal) error {
	if !state.Owns(pid, pgid, startTime) {
		return fmt.Errorf("refusing to signal pid %d: identity mismatch (not owned by tora)", pid)
	}
	return syscall.Kill(-pgid, sig)
}

// GroupAlive reports whether any process in pgid is still alive ( post-verify).
func GroupAlive(pgid int) bool {
	return syscall.Kill(-pgid, 0) == nil
}

// WaitGone polls until the process group is gone or timeout elapses.
func WaitGone(pgid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !GroupAlive(pgid) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return !GroupAlive(pgid)
}

// Stop performs graceful shutdown: SIGTERM, wait, SIGKILL fallback.
// Ownership is re-verified before each signal.
func Stop(pid, pgid int, startTime string, timeout time.Duration) (killed bool, err error) {
	if !state.Owns(pid, pgid, startTime) {
		// Not ours: if the recorded pid is simply gone, treat as already stopped.
		if !state.Inspect(pid).Alive {
			return false, nil
		}
		return false, fmt.Errorf("refusing to stop pid %d: identity mismatch (not owned by tora)", pid)
	}
	if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil {
		if !state.Owns(pid, pgid, startTime) {
			return false, nil
		}
		return false, fmt.Errorf("SIGTERM pgid %d: %w", pgid, err)
	}
	if WaitGone(pgid, timeout) {
		return false, nil
	}
	if !state.Owns(pid, pgid, startTime) {
		return false, nil
	}
	if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil {
		return false, fmt.Errorf("SIGKILL pgid %d: %w", pgid, err)
	}
	WaitGone(pgid, 5*time.Second)
	return true, nil
}
