package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Find locates tora.yaml from dir upward. Returns "" when absent.
func Find(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		p := filepath.Join(abs, "tora.yaml")
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return ""
		}
		abs = parent
	}
}

// Load reads, parses (strict), defaults, and validates a config file.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("invalid YAML: %w", err)
	}
	c.applyDefaults()
	if errs := Validate(&c); len(errs) > 0 {
		msg := "invalid configuration:"
		for _, e := range errs {
			msg += "\n  - " + e.Error()
		}
		return nil, fmt.Errorf("%s", msg)
	}
	return &c, nil
}
