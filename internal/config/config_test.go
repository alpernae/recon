package config

import (
	"path/filepath"
	"testing"
)

func TestParseModeRejectsConflicts(t *testing.T) {
	t.Parallel()

	if _, err := ParseMode(true, true); err == nil {
		t.Fatal("expected conflicting flags to fail")
	}
}

func TestResolveOutDirUsesCWDForRelativePaths(t *testing.T) {
	t.Parallel()

	cwd := filepath.Join("root", "workspace")
	got := ResolveOutDir(cwd, "example.com", "custom-output")
	want := filepath.Join(cwd, "custom-output")
	if got != want {
		t.Fatalf("expected %s, got %s", want, got)
	}
}
