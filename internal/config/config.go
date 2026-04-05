package config

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
)

type Mode string

const (
	ModeFast   Mode = "fast"
	ModeNormal Mode = "normal"
	ModeDeep   Mode = "deep"
)

type Runtime string

const (
	RuntimeAuto   Runtime = "auto"
	RuntimeNative Runtime = "native"
	RuntimeDocker Runtime = "docker"
)

const DefaultDockerImage = "recon-framework:latest"

type CommonOptions struct {
	CWD                     string
	ToolRoot                string
	Runtime                 Runtime
	ResolversPath           string
	ChaosAPIKey             string
	SubfinderProviderConfig string
	AmassConfig             string
	DockerImage             string
}

type RunOptions struct {
	CommonOptions
	Domain  string
	Mode    Mode
	OutDir  string
	Threads int
	Rate    int
	// UI and output options
	Verbose       bool
	Color         bool
	Screenshot    bool
	ScreenshotDir string
}

type DoctorOptions struct {
	CommonOptions
	OutDir string
}

type InstallOptions struct {
	CommonOptions
	IncludeOptional bool
	Force           bool
	TargetOS        string
	TargetArch      string
	Global          bool
}

type RuntimeDecision struct {
	Selected   Runtime
	Reason     string
	HighParity bool
}

func DefaultToolRoot(cwd string) string {
	return filepath.Join(cwd, ".tools", runtime.GOOS+"-"+runtime.GOARCH)
}

func ToolBinDir(toolRoot string) string {
	return filepath.Join(toolRoot, "bin")
}

func DefaultResolversPath(cwd string) string {
	return filepath.Join(cwd, "resolvers.txt")
}

func ParseMode(fast, deep bool) (Mode, error) {
	if fast && deep {
		return "", fmt.Errorf("--fast and --deep cannot be used together")
	}
	if fast {
		return ModeFast, nil
	}
	if deep {
		return ModeDeep, nil
	}
	return ModeNormal, nil
}

func ParseRuntime(value string) (Runtime, error) {
	switch Runtime(strings.ToLower(strings.TrimSpace(value))) {
	case RuntimeAuto:
		return RuntimeAuto, nil
	case RuntimeNative:
		return RuntimeNative, nil
	case RuntimeDocker:
		return RuntimeDocker, nil
	default:
		return "", fmt.Errorf("unsupported runtime %q", value)
	}
}

func ModeProfile(mode Mode) (threads, rate int) {
	switch mode {
	case ModeFast:
		return 50, 200
	case ModeDeep:
		return 250, 1000
	default:
		return 200, 800
	}
}

func ResolveOutDir(cwd, domain, outDir string) string {
	if strings.TrimSpace(outDir) == "" {
		return filepath.Join(cwd, "output-"+domain)
	}
	if filepath.IsAbs(outDir) {
		return outDir
	}
	return filepath.Join(cwd, outDir)
}
