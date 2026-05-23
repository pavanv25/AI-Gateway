package alias

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Entry is a single provider+model pair in an alias fallback list.
type Entry struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
}

// config holds the parsed alias file contents.
type config struct {
	Tasks map[string][]Entry `yaml:"tasks"`
}

// Resolver resolves task names to ordered Entry slices.
// A nil Resolver means the alias feature is disabled.
type Resolver struct {
	cfg config
}

// Load reads the YAML file at path and returns a Resolver.
// Returns (nil, nil) if path is empty — alias feature silently disabled.
// Returns an error if the file exists but cannot be read or parsed.
func Load(path string) (*Resolver, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("alias: read %q: %w", path, err)
	}
	var cfg config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("alias: parse %q: %w", path, err)
	}
	if cfg.Tasks == nil {
		cfg.Tasks = make(map[string][]Entry)
	}
	return &Resolver{cfg: cfg}, nil
}

// Resolve returns the ordered Entry list for the given task name.
// Returns an error if the task is unknown, empty, or if r is nil.
func (r *Resolver) Resolve(task string) ([]Entry, error) {
	if r == nil {
		return nil, fmt.Errorf("alias feature is not configured (ALIAS_CONFIG not set)")
	}
	entries, ok := r.cfg.Tasks[task]
	if !ok {
		return nil, fmt.Errorf("unknown task %q", task)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("task %q has no entries", task)
	}
	return entries, nil
}

// Enabled reports whether the resolver is active. Safe to call on nil.
func (r *Resolver) Enabled() bool {
	return r != nil
}
