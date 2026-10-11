package state

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// ProcInfo describes a live Linux process used to verify ownership.
type ProcInfo struct {
	Alive     bool
	StartTime string // kernel starttime tick, stable across the process lifetime
	PGID      int
	Cmdline   string
}

// Inspect reads /proc/pid/stat and /proc/pid/cmdline.
func Inspect(pid int) ProcInfo {
	if pid <= 0 {
		return ProcInfo{}
	}
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return ProcInfo{}
	}
	s := string(raw)
	// comm may contain spaces/parens; fields after the last ')' are stable.
	i := strings.LastIndex(s, ")")
	if i < 0 {
		return ProcInfo{}
	}
	fields := strings.Fields(s[i+1:])
	// After comm: state(1) ppid(2) pgrp(3) ... starttime(20, 1-based after comm).
	if len(fields) < 20 {
		return ProcInfo{}
	}
	pgid, _ := strconv.Atoi(fields[2])
	cmd, _ := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	return ProcInfo{
		Alive:     true,
		StartTime: fields[19],
		PGID:      pgid,
		Cmdline:   strings.ReplaceAll(string(cmd), "\x00", " "),
	}
}

// Owns reports whether pid is still our process: alive, same kernel
// starttime tick, and same process group.
//
// PID+starttime is the load-bearing identity: the kernel assigns a fresh
// starttime on every process creation, so a recycled PID can never match.
// PGID additionally scopes signals to our own group. The executable path is
// deliberately NOT part of identity: a service launcher (sh -c, env, wrappers)
// may exec into a different binary while remaining the same process.
func Owns(pid, pgid int, startTime string) bool {
	info := Inspect(pid)
	return info.Alive && info.StartTime == startTime && info.PGID == pgid
}
