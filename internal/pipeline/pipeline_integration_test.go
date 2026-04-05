package pipeline

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/alpernae/recon/internal/config"
	"github.com/alpernae/recon/internal/toolchain"
	"github.com/alpernae/recon/internal/util"
)

func TestPipelineRunWithMockedExecutables(t *testing.T) {
	cwd := t.TempDir()
	toolRoot := filepath.Join(cwd, ".tools", runtime.GOOS+"-"+runtime.GOARCH)
	binDir := config.ToolBinDir(toolRoot)
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, tool := range []string{"amass", "subfinder", "chaos", "gau", "dnsx", "httpx", "nuclei"} {
		installMockTool(t, binDir, tool)
	}

	if err := util.WriteTextLinesAtomic(filepath.Join(cwd, "resolvers.txt"), []string{"1.1.1.1"}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cwd, "wordlists"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := util.WriteTextLinesAtomic(filepath.Join(cwd, "wordlists", "base_tokens.txt"), []string{"portal", "auth"}); err != nil {
		t.Fatal(err)
	}

	tc := toolchain.New(binDir)
	pipe := New(tc, io.Discard, io.Discard)
	opts := config.RunOptions{
		CommonOptions: config.CommonOptions{
			CWD:           cwd,
			ToolRoot:      toolRoot,
			Runtime:       config.RuntimeNative,
			ResolversPath: filepath.Join(cwd, "resolvers.txt"),
			ChaosAPIKey:   "test-key",
			DockerImage:   config.DefaultDockerImage,
		},
		Domain:  "example.com",
		Mode:    config.ModeFast,
		OutDir:  filepath.Join(cwd, "output-example.com"),
		Threads: 50,
		Rate:    200,
	}

	t.Setenv("GO_WANT_HELPER_PROCESS", "1")

	run := func() {
		if err := pipe.Run(context.Background(), opts, config.RuntimeDecision{
			Selected:   config.RuntimeNative,
			HighParity: false,
		}); err != nil {
			t.Fatalf("pipeline run failed: %v", err)
		}
	}

	run()
	run()

	for _, dir := range []string{"passive", "resolved", "perms", "intel", "live", "scan"} {
		if _, err := os.Stat(filepath.Join(opts.OutDir, dir)); err != nil {
			t.Fatalf("expected output directory %s: %v", dir, err)
		}
	}

	liveLines, err := util.ReadLines(filepath.Join(opts.OutDir, "live", "live.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(liveLines) == 0 {
		t.Fatal("expected live hosts")
	}

	nucleiLines, err := util.ReadLines(filepath.Join(opts.OutDir, "scan", "nuclei.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(nucleiLines) == 0 {
		t.Fatal("expected nuclei findings")
	}
}

func installMockTool(t *testing.T, binDir, name string) {
	t.Helper()

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	destination := filepath.Join(binDir, name)
	if runtime.GOOS == "windows" {
		destination += ".exe"
	}
	if err := util.CopyFile(executable, destination); err != nil {
		t.Fatal(err)
	}
}

func TestMain(m *testing.M) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		os.Exit(m.Run())
	}

	toolName := strings.TrimSuffix(strings.ToLower(filepath.Base(os.Args[0])), ".exe")
	switch toolName {
	case "amass":
		mustWriteFlagFile("-o", "www.example.com\napi.example.com\n")
	case "subfinder":
		mustWriteFlagFile("-o", "api.example.com\ndev.example.com\n")
	case "chaos":
		mustWriteFlagFile("-o", "edge.example.com\n")
	case "gau":
		_, _ = os.Stdout.WriteString("https://portal.example.com/auth/login\nhttps://api.example.com/v1/users\nhttps://archive.example.com/reports/2024\n")
	case "dnsx", "shuffledns":
		listFlag := "-l"
		if toolName == "shuffledns" {
			listFlag = "-list"
		}
		input := flagValue(listFlag)
		lines, err := util.ReadLines(input)
		must(err)
		for _, line := range lines {
			_, _ = os.Stdout.WriteString(line + "\n")
		}
	case "httpx":
		input := flagValue("-l")
		output := flagValue("-o")
		lines, err := util.ReadLines(input)
		must(err)
		live := make([]string, 0, len(lines))
		for _, line := range lines {
			live = append(live, "https://"+line)
		}
		must(util.WriteTextLinesAtomic(output, live))
	case "nuclei":
		input := flagValue("-l")
		output := flagValue("-o")
		lines, err := util.ReadLines(input)
		must(err)
		if len(lines) == 0 {
			os.Exit(1)
		}
		must(util.WriteTextLinesAtomic(output, []string{"[mock] info " + lines[0]}))
	default:
		os.Exit(1)
	}

	os.Exit(0)
}

func mustWriteFlagFile(flagName, content string) {
	output := flagValue(flagName)
	must(util.WriteStringAtomic(output, content, false))
}

func flagValue(name string) string {
	for index := 1; index < len(os.Args)-1; index++ {
		if os.Args[index] == name {
			return os.Args[index+1]
		}
	}
	return ""
}

func must(err error) {
	if err != nil {
		os.Exit(1)
	}
}
