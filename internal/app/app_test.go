package app

import (
	"bytes"
	"context"
	"testing"
)

func TestCLIReturnsErrorForConflictingModeFlags(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cli := NewCLI(&stdout, &stderr)
	code := cli.Run(context.Background(), []string{"example.com", "--fast", "--deep"})
	if code == 0 {
		t.Fatal("expected CLI to reject conflicting mode flags")
	}
}

func TestToContainerPathRejectsExternalPath(t *testing.T) {
	t.Parallel()

	cwd := t.TempDir()
	external := t.TempDir()
	if _, err := toContainerPath(cwd, external); err == nil {
		t.Fatal("expected external path to be rejected")
	}
}
