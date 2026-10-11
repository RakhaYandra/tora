package state

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Lock guards one project against concurrent managers via flock.
type Lock struct {
	f *os.File
}

// Acquire takes an exclusive non-blocking lock on <stateDir>/lock.
// The lock releases when the process exits or Release is called.
func Acquire(stateDir string) (*Lock, error) {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(stateDir, "lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another tora instance is managing this project (lock held): %w", err)
	}
	fmt.Fprintf(f, "%d\n", os.Getpid())
	return &Lock{f: f}, nil
}

// Release unlocks and closes.
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	return l.f.Close()
}
