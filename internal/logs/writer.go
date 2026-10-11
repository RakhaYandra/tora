// Package logs captures service output to files and reads it back with
// tail and follow support. Stored content is never mutated for display;
// service names and timestamps are added only at presentation time.
package logs

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	// MaxSize caps a log file before rotation.
	MaxSize = 10 << 20 // 10 MiB
	// Keep is the number of rotated backups (.1 .. .Keep).
	Keep = 3
)

// Path returns the log file for a service.
func Path(logDir, service string) string {
	return filepath.Join(logDir, service+".log")
}

// RotateIfNeeded shifts <name>.log -> <name>.log.1 .. when over MaxSize.
func RotateIfNeeded(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if st.Size() <= MaxSize {
		return nil
	}
	oldest := fmt.Sprintf("%s.%d", path, Keep)
	_ = os.Remove(oldest)
	for i := Keep - 1; i >= 1; i-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", path, i), fmt.Sprintf("%s.%d", path, i+1))
	}
	return os.Rename(path, path+".1")
}
