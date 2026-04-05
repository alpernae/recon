package toolchain

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"recon-framework/internal/config"
)

func TestFindPrefersLocalToolsOverPATH(t *testing.T) {
	t.Parallel()

	binDir := t.TempDir()
	localTool := filepath.Join(binDir, "dnsx")
	if runtime.GOOS == "windows" {
		localTool += ".exe"
	}
	if err := os.WriteFile(localTool, []byte("mock"), 0o755); err != nil {
		t.Fatal(err)
	}

	tc := New(binDir)
	tc.LookPath = func(string) (string, error) {
		return filepath.Join("C:", "tools", "dnsx.exe"), nil
	}

	status := tc.Find(ToolSpec{Name: "dnsx", Binary: "dnsx"})
	if !status.Available {
		t.Fatal("expected dnsx to be available")
	}
	if status.Source != "local-tools" {
		t.Fatalf("expected local-tools source, got %s", status.Source)
	}
	if status.Path != localTool {
		t.Fatalf("expected %s, got %s", localTool, status.Path)
	}
}

func TestResolveRuntimeAutoWindowsPrefersDockerForParity(t *testing.T) {
	t.Parallel()

	binDir := t.TempDir()
	for _, spec := range KnownTools() {
		if !spec.Required {
			continue
		}
		path := filepath.Join(binDir, spec.Binary)
		if runtime.GOOS == "windows" {
			path += ".exe"
		}
		if err := os.WriteFile(path, []byte("mock"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	tc := New(binDir)
	tc.OS = "windows"
	tc.HasDocker = func(context.Context) bool { return true }
	tc.HasImage = func(context.Context, string) bool { return true }

	decision := tc.ResolveRuntime(context.Background(), config.CommonOptions{
		Runtime:     config.RuntimeAuto,
		DockerImage: config.DefaultDockerImage,
	})

	if decision.Selected != config.RuntimeDocker {
		t.Fatalf("expected docker runtime, got %s", decision.Selected)
	}
}

func TestBuildDoctorReportWarnsWhenWindowsAmassEngineIsMissing(t *testing.T) {
	t.Parallel()

	binDir := t.TempDir()
	amassPath := filepath.Join(binDir, "amass.exe")
	if err := os.WriteFile(amassPath, []byte("mock"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"subfinder.exe", "chaos.exe", "gau.exe", "dnsx.exe", "httpx.exe", "nuclei.exe", "shuffledns.exe"} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte("mock"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	resolvers := filepath.Join(t.TempDir(), "resolvers.txt")
	if err := os.WriteFile(resolvers, []byte("1.1.1.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tc := New(binDir)
	tc.OS = "windows"
	tc.HasDocker = func(context.Context) bool { return false }
	tc.HasImage = func(context.Context, string) bool { return false }

	report := tc.BuildDoctorReport(context.Background(), config.CommonOptions{
		Runtime:       config.RuntimeNative,
		ResolversPath: resolvers,
		DockerImage:   config.DefaultDockerImage,
	})

	if len(report.Errors) != 0 {
		t.Fatalf("expected no blocking errors, got %v", report.Errors)
	}

	found := false
	for _, warning := range report.Warnings {
		if strings.Contains(warning, "engine.exe is missing") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected engine warning, got %v", report.Warnings)
	}
}
