package config

import (
	"os"
	"path/filepath"
	"testing"
)

func valid() *Config {
	c := &Config{
		Version: 1,
		Project: Project{Name: "pulse"},
		Services: map[string]Service{
			"api":    {Command: []string{"go", "run", "./cmd/api"}, DependsOn: []string{"db"}},
			"db":     {Command: []string{"postgres"}},
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
