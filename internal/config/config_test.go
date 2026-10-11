package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func valid() *Config {
	c := &Config{
		Version: 1,
		Project: Project{Name: "pulse"},
		Services: map[string]Service{
			"api":    {Command: []string{"go", "run", "./cmd/api"}, DependsOn: []string{"db"}},
			"db":     {Command: []string{"sleep", "30"}},
			"worker": {Command: []string{"go", "run", "./cmd/worker"}, Restart: "on-failure"},
		},
	}
	c.applyDefaults()
	return c
}

func TestValidateOK(t *testing.T) {
	if errs := Validate(valid()); len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
}

func TestValidateCases(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"bad version", func(c *Config) { c.Version = 2 }},
		{"bad project name", func(c *Config) { c.Project.Name = "Bad Name!" }},
		{"empty services", func(c *Config) { c.Services = map[string]Service{} }},
		{"empty command", func(c *Config) {
			s := c.Services["api"]
			s.Command = nil
			c.Services["api"] = s
		}},
		{"bad restart", func(c *Config) {
			s := c.Services["api"]
			s.Restart = "always"
			c.Services["api"] = s
		}},
		{"unknown dep", func(c *Config) {
			s := c.Services["api"]
			s.DependsOn = []string{"ghost"}
			c.Services["api"] = s
		}},
		{"self dep", func(c *Config) {
			s := c.Services["api"]
			s.DependsOn = []string{"api"}
			c.Services["api"] = s
		}},
		{"dup dep", func(c *Config) {
			s := c.Services["api"]
			s.DependsOn = []string{"db", "db"}
			c.Services["api"] = s
		}},
		{"bad timeout", func(c *Config) { c.Runtime.ShutdownTimeout = 0 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := valid()
			tc.mutate(c)
			if errs := Validate(c); len(errs) == 0 {
				t.Fatal("expected validation errors")
			}
		})
	}
}

func TestLoadStrictRejectsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "tora.yaml")
	os.WriteFile(p, []byte("version: 1\nproject:\n  name: pulse\nruntime: {}\nservices:\n  api:\n    command: [\"true\"]\n    bogus_field: 1\n"), 0o644)
	if _, err := Load(p); err == nil {
		t.Fatal("expected strict-mode error for unknown field")
	}
}

func TestLoadBadYAML(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "tora.yaml")
	os.WriteFile(p, []byte("version: [unclosed\n"), 0o644)
	if _, err := Load(p); err == nil {
		t.Fatal("expected YAML syntax error")
	}
}

func TestResolveWorkDir(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "web"), 0o755)
	c := valid()
	c.Services["web"] = Service{Command: []string{"npm", "run", "dev"}, WorkingDir: "./web"}
	c.applyDefaults()
	l, err := Resolve(c, dir)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if l.WorkDirs["web"] != filepath.Join(dir, "web") {
		t.Fatalf("unexpected workdir %q", l.WorkDirs["web"])
	}
	c.Services["web"] = Service{Command: []string{"x"}, WorkingDir: "./missing"}
	if _, err := Resolve(c, dir); err == nil {
		t.Fatal("expected error for missing working dir")
	}
}

func TestFind(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "a", "b")
	os.MkdirAll(sub, 0o755)
	os.WriteFile(filepath.Join(dir, "tora.yaml"), []byte("version: 1"), 0o644)
	if got := Find(sub); got != filepath.Join(dir, "tora.yaml") {
		t.Fatalf("find = %q", got)
	}
}

func TestValidateExecutableAndPORT(t *testing.T) {
	c := valid()
	s := c.Services["api"]
	s.Command = []string{"definitely-not-a-real-binary-xyz"}
	c.Services["api"] = s
	found := false
	for _, e := range Validate(c) {
		if contains(e.Error(), "not found in PATH") {
			found = true
		}
	}
	if !found {
		t.Fatal("expected missing-executable error")
	}
	c = valid()
	a := c.Services["api"]
	a.Env = map[string]string{"PORT": "8080"}
	c.Services["api"] = a
	w := c.Services["worker"]
	w.Env = map[string]string{"PORT": "8080"}
	c.Services["worker"] = w
	found = false
	for _, e := range Validate(c) {
		if contains(e.Error(), "already claimed") {
			found = true
		}
	}
	if !found {
		t.Fatal("expected PORT collision error")
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func TestValidateHealthcheck(t *testing.T) {
	mk := func() *Config {
		c := valid()
		s := c.Services["api"]
		s.Healthcheck = &Healthcheck{Type: "http", Port: 8080}
		c.Services["api"] = s
		c.applyDefaults()
		return c
	}
	if errs := Validate(mk()); len(errs) != 0 {
		t.Fatalf("valid healthcheck rejected: %v", errs)
	}
	if h := mk().Services["api"].Healthcheck; h.Host != "127.0.0.1" || h.Path != "/" {
		t.Fatalf("defaults not applied: %+v", h)
	}
	cases := []struct {
		name   string
		mutate func(*Healthcheck)
	}{
		{"bad type", func(h *Healthcheck) { h.Type = "grpc" }},
		{"bad port", func(h *Healthcheck) { h.Port = 99999 }},
		{"bad path", func(h *Healthcheck) { h.Path = "health" }},
		{"tcp with path", func(h *Healthcheck) { h.Type = "tcp"; h.Path = "/x" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := mk()
			s := c.Services["api"]
			tc.mutate(s.Healthcheck)
			c.Services["api"] = s
			if errs := Validate(c); len(errs) == 0 {
				t.Fatal("expected validation error")
			}
		})
	}
}
