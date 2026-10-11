package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"
)

var serviceNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-_]{1,62}$`)

// Loaded bundles a validated config with resolved absolute paths.
type Loaded struct {
	Config     *Config
	Path       string // absolute path of tora.yaml
	ProjectDir string // absolute project root
	LogDir     string // absolute
	StateDir   string // absolute
	WorkDirs   map[string]string
}

// Validate checks structure and semantics. It never spawns processes.
func Validate(c *Config) []error {
	var errs []error
	fail := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf(format, args...))
	}
	if c.Version != 1 {
		fail("version: must be 1, got %d", c.Version)
	}
	if !serviceNameRe.MatchString(c.Project.Name) {
		fail("project.name: %q must match %s", c.Project.Name, serviceNameRe.String())
	}
	if c.Services == nil || len(c.Services) == 0 {
		fail("services: at least one service is required")
	}
	for name, s := range c.Services {
		p := "services." + name
		if !serviceNameRe.MatchString(name) {
			fail("%s: invalid service name %q", p, name)
		}
		if len(s.Command) == 0 {
			fail("%s.command: required, must be a non-empty argv array (no shell strings)", p)
		} else if _, err := exec.LookPath(s.Command[0]); err != nil {
			fail("%s.command: executable %q not found in PATH", p, s.Command[0])
		}
		for _, arg := range s.Command {
			if arg == "" {
				fail("%s.command: arguments must not be empty", p)
			}
		}
		if s.Restart != "no" && s.Restart != "on-failure" {
			fail("%s.restart: must be \"no\" or \"on-failure\", got %q", p, s.Restart)
		}
		seen := map[string]bool{}
		for _, d := range s.DependsOn {
			if d == name {
				fail("%s.depends_on: service cannot depend on itself", p)
			}
			if seen[d] {
				fail("%s.depends_on: duplicate dependency %q", p, d)
			}
			seen[d] = true
			if _, ok := c.Services[d]; !ok {
				fail("%s.depends_on: unknown service %q", p, d)
			}
		}
	}
	if c.Runtime.StartupTimeout.Std() <= 0 || c.Runtime.StartupTimeout.Std() > 10*time.Minute {
		fail("runtime.startup_timeout: must be positive and at most 10m")
	}
	if c.Runtime.ShutdownTimeout.Std() <= 0 || c.Runtime.ShutdownTimeout.Std() > 10*time.Minute {
		fail("runtime.shutdown_timeout: must be positive and at most 10m")
	}
	if c.Runtime.LogDir == "" || c.Runtime.StateDir == "" {
		fail("runtime: log_dir and state_dir must not be empty")
	}
	// PORT collision hint: two services claiming the same PORT env cannot
	// both bind it. Advisory of intent, not a live port probe.
	ports := map[string]string{}
	for name, s := range c.Services {
		if port, ok := s.Env["PORT"]; ok && port != "" {
			if owner, dup := ports[port]; dup {
				fail("services.%s.env.PORT: %q already claimed by services.%s", name, port, owner)
			} else {
				ports[port] = name
			}
		}
	}
	return errs
}

// Resolve validates working directories against projectDir and fills absolute paths.
func Resolve(c *Config, projectDir string) (*Loaded, error) {
	workDirs := make(map[string]string, len(c.Services))
	for name, s := range c.Services {
		abs := s.WorkingDir
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(projectDir, s.WorkingDir)
		}
		abs = filepath.Clean(abs)
		st, err := os.Stat(abs)
		if err != nil || !st.IsDir() {
			fail := abs
			if err != nil {
				fail = fmt.Sprintf("%s (%v)", abs, err)
			}
			return nil, fmt.Errorf("services.%s.working_dir: directory unavailable: %s", name, fail)
		}
		workDirs[name] = abs
	}
	logDir := c.Runtime.LogDir
	if !filepath.IsAbs(logDir) {
		logDir = filepath.Join(projectDir, logDir)
	}
	stateDir := c.Runtime.StateDir
	if !filepath.IsAbs(stateDir) {
		stateDir = filepath.Join(projectDir, stateDir)
	}
	logDir, stateDir = filepath.Clean(logDir), filepath.Clean(stateDir)
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, fmt.Errorf("runtime.log_dir: cannot create %s: %w", logDir, err)
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, fmt.Errorf("runtime.state_dir: cannot create %s: %w", stateDir, err)
	}
	return &Loaded{Config: c, ProjectDir: projectDir, LogDir: logDir, StateDir: stateDir, WorkDirs: workDirs}, nil
}
