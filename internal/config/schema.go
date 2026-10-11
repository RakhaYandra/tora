package config

import (
	"fmt"
	"time"
)

// Duration is a time.Duration parsed from YAML strings like "30s".
type Duration time.Duration

func (d *Duration) UnmarshalYAML(unmarshal func(any) error) error {
	var s string
	if err := unmarshal(&s); err != nil {
		return fmt.Errorf("must be a duration string like \"30s\"")
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(v)
	return nil
}

func (d Duration) Std() time.Duration { return time.Duration(d) }

// Config is the root of tora.yaml. version selects the schema, not the binary.
type Config struct {
	Version  int                `yaml:"version"`
	Project  Project            `yaml:"project"`
	Runtime  Runtime            `yaml:"runtime"`
	Services map[string]Service `yaml:"services"`
}

type Project struct {
	Name string `yaml:"name"`
}

type Runtime struct {
	StartupTimeout  Duration `yaml:"startup_timeout"`
	ShutdownTimeout Duration `yaml:"shutdown_timeout"`
	LogDir          string   `yaml:"log_dir"`
	StateDir        string   `yaml:"state_dir"`
}

type Service struct {
	Command     []string          `yaml:"command"`
	WorkingDir  string            `yaml:"working_dir"`
	DependsOn   []string          `yaml:"depends_on"`
	Env         map[string]string `yaml:"env"`
	Restart     string            `yaml:"restart"`
	Healthcheck *Healthcheck      `yaml:"healthcheck"`
}

// Healthcheck gates dependents: a service is RUNNING only after its check
// passes (within runtime.startup_timeout). Absent = order-only (v0.1 watch).
type Healthcheck struct {
	Type     string   `yaml:"type"`     // tcp | http (required when block present)
	Host     string   `yaml:"host"`     // default 127.0.0.1
	Port     int      `yaml:"port"`     // required, 1-65535
	Path     string   `yaml:"path"`     // http only, default /
	Interval Duration `yaml:"interval"` // default 500ms
	Timeout  Duration `yaml:"timeout"`  // per attempt, default 2s
}

// Defaults applied when fields are absent.
const (
	DefaultStartupTimeout  = 30 * time.Second
	DefaultShutdownTimeout = 10 * time.Second
	DefaultLogDir          = ".tora/logs"
	DefaultStateDir        = ".tora"
	DefaultRestart         = "no"

	DefaultHealthHost     = "127.0.0.1"
	DefaultHealthPath     = "/"
	DefaultHealthInterval = 500 * time.Millisecond
	DefaultHealthTimeout  = 2 * time.Second
)

func (c *Config) applyDefaults() {
	if c.Runtime.StartupTimeout == 0 {
		c.Runtime.StartupTimeout = Duration(DefaultStartupTimeout)
	}
	if c.Runtime.ShutdownTimeout == 0 {
		c.Runtime.ShutdownTimeout = Duration(DefaultShutdownTimeout)
	}
	if c.Runtime.LogDir == "" {
		c.Runtime.LogDir = DefaultLogDir
	}
	if c.Runtime.StateDir == "" {
		c.Runtime.StateDir = DefaultStateDir
	}
	for name, s := range c.Services {
		if s.WorkingDir == "" {
			s.WorkingDir = "."
		}
		if s.Restart == "" {
			s.Restart = DefaultRestart
		}
		if s.Env == nil {
			s.Env = map[string]string{}
		}
		if s.DependsOn == nil {
			s.DependsOn = []string{}
		}
		if s.Healthcheck != nil {
			h := s.Healthcheck
			if h.Host == "" {
				h.Host = DefaultHealthHost
			}
			if h.Type == "http" && h.Path == "" {
				h.Path = DefaultHealthPath
			}
			if h.Interval == 0 {
				h.Interval = Duration(DefaultHealthInterval)
			}
			if h.Timeout == 0 {
				h.Timeout = Duration(DefaultHealthTimeout)
			}
		}
		c.Services[name] = s
	}
}
