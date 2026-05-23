package alias

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "aliases-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(content)
	f.Close()
	return f.Name()
}

func TestLoad_EmptyPath_ReturnsNilResolver(t *testing.T) {
	r, err := Load("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r != nil {
		t.Error("expected nil resolver for empty path")
	}
}

func TestLoad_FileNotFound_ReturnsError(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nonexistent.yaml"))
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLoad_InvalidYAML_ReturnsError(t *testing.T) {
	path := writeTemp(t, "tasks: {not: valid yaml: [}")
	_, err := Load(path)
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestLoad_ValidYAML_ResolvesEntries(t *testing.T) {
	path := writeTemp(t, `
tasks:
  fast-chat:
    - provider: openai
      model: gpt-4o-mini
    - provider: anthropic
      model: claude-haiku-4-5-20251001
`)
	r, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r == nil {
		t.Fatal("expected non-nil resolver")
	}
	entries, err := r.Resolve("fast-chat")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entry count: got %d, want 2", len(entries))
	}
	if entries[0].Provider != "openai" || entries[0].Model != "gpt-4o-mini" {
		t.Errorf("entry[0]: got %+v", entries[0])
	}
	if entries[1].Provider != "anthropic" {
		t.Errorf("entry[1].Provider: got %q, want anthropic", entries[1].Provider)
	}
}

func TestLoad_EmptyTasksBlock_NoError(t *testing.T) {
	path := writeTemp(t, "tasks:\n")
	r, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r == nil {
		t.Fatal("expected non-nil resolver")
	}
}

func TestResolver_Resolve_UnknownTask(t *testing.T) {
	path := writeTemp(t, "tasks:\n  known:\n    - provider: mock\n      model: m\n")
	r, _ := Load(path)
	_, err := r.Resolve("unknown")
	if err == nil {
		t.Error("expected error for unknown task")
	}
}

func TestResolver_Resolve_EmptyEntryList(t *testing.T) {
	path := writeTemp(t, "tasks:\n  empty: []\n")
	r, _ := Load(path)
	_, err := r.Resolve("empty")
	if err == nil {
		t.Error("expected error for empty entry list")
	}
}

func TestResolver_Nil_ReturnsError(t *testing.T) {
	var r *Resolver
	_, err := r.Resolve("anything")
	if err == nil {
		t.Error("expected error on nil resolver")
	}
}

func TestResolver_Enabled_NilReturnsFalse(t *testing.T) {
	var r *Resolver
	if r.Enabled() {
		t.Error("nil resolver should not be Enabled")
	}
}

func TestResolver_Enabled_LoadedReturnsTrue(t *testing.T) {
	path := writeTemp(t, "tasks:\n")
	r, _ := Load(path)
	if !r.Enabled() {
		t.Error("loaded resolver should be Enabled")
	}
}
