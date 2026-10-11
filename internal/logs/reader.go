package logs

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// Tail returns the last n lines of path (n<=0 means all). Missing file -> empty.
func Tail(path string, n int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}

// streamer follows one file, calling emit for each new line.
type streamer struct {
	path   string
	prefix string // display prefix, e.g. "[api] " or "[12:01:02] [api] "
	offset int64
	f      *os.File
	match  func(raw string) bool // nil keeps all; applied pre-prefix
}

// Grep keeps lines containing pattern (substring). Empty pattern keeps all.
func Grep(lines []string, pattern string, ignoreCase bool) []string {
	if pattern == "" {
		return lines
	}
	if ignoreCase {
		pattern = strings.ToLower(pattern)
	}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		hay := l
		if ignoreCase {
			hay = strings.ToLower(l)
		}
		if strings.Contains(hay, pattern) {
			out = append(out, l)
		}
	}
	return out
}

func (s *streamer) open() error {
	f, err := os.Open(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	s.f = f
	return nil
}

// CatchUp emits lines after offset; on rotation (shrink) it restarts at 0.
func (s *streamer) CatchUp(emit func(string)) error {
	if s.f == nil {
		if err := s.open(); err != nil {
			return err
		}
		if s.f == nil {
			return nil
		}
	}
	st, err := s.f.Stat()
	if err != nil {
		s.f.Close()
		s.f = nil
		return nil
	}
	if st.Size() < s.offset {
		s.f.Close()
		s.f = nil
		s.offset = 0
		return s.CatchUp(emit)
	}
	if _, err := s.f.Seek(s.offset, io.SeekStart); err != nil {
		return err
	}
	rd := bufio.NewReader(s.f)
	for {
		line, err := rd.ReadString('\n')
		if len(line) > 0 {
			raw := strings.TrimRight(line, "\n")
			if s.match == nil || s.match(raw) {
				emit(s.prefix + raw)
			}
		}
		if err != nil {
			break
		}
	}
	off, _ := s.f.Seek(0, io.SeekCurrent)
	s.offset = off
	return nil
}

// Follow prints existing tail then streams appended lines until ctx ends.
// names selects services; when combined, lines carry timestamp+service prefix.
// grep filters raw lines (substring, optional case-insensitive); "" disables.
func Follow(ctx context.Context, logDir string, names []string, tailN int, combined bool, grep string, ignoreCase bool, out io.Writer) error {
	match := func(raw string) bool {
		return len(Grep([]string{raw}, grep, ignoreCase)) == 1
	}
	ss := make([]*streamer, len(names))
	for i, n := range names {
		prefix := "[" + n + "] "
		if combined {
			prefix = "" // timestamped per line below
		}
		ss[i] = &streamer{path: Path(logDir, n), prefix: prefix, match: match}
		existing, err := Tail(ss[i].path, tailN)
		if err != nil {
			return err
		}
		for _, l := range Grep(existing, grep, ignoreCase) {
			if combined {
				fmt.Fprintf(out, "[%s] [%s] %s\n", time.Now().Format("15:04:05"), n, l)
			} else {
				fmt.Fprintf(out, "%s%s\n", prefix, l)
			}
		}
		if st, err := os.Stat(ss[i].path); err == nil {
			ss[i].offset = st.Size()
		}
		if err := ss[i].open(); err != nil {
			return err
		}
	}
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			for i, s := range ss {
				p := s.prefix
				if combined {
					ts := time.Now().Format("15:04:05")
					s.prefix = "[" + ts + "] [" + names[i] + "] "
				}
				_ = s.CatchUp(func(l string) { fmt.Fprintln(out, l) })
				s.prefix = p
			}
		}
	}
}
