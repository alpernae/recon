package toolchain

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/alpernae/recon/internal/config"
)

type ToolSpec struct {
	Name        string
	Binary      string
	GoModule    string
	GitHubRepo  string
	Required    bool
	Optional    bool
	Description string
}

type ToolStatus struct {
	Spec      ToolSpec
	Path      string
	Available bool
	Source    string
}

type DoctorReport struct {
	Runtime         config.RuntimeDecision
	Statuses        []ToolStatus
	Warnings        []string
	Errors          []string
	DockerAvailable bool
	ImageAvailable  bool
}

type Toolchain struct {
	BinDir    string
	LookPath  func(string) (string, error)
	ExecPath  string
	OS        string
	Arch      string
	HasDocker func(context.Context) bool
	HasImage  func(context.Context, string) bool
}

func New(binDir string) *Toolchain {
	return &Toolchain{
		BinDir:   binDir,
		LookPath: exec.LookPath,
		ExecPath: os.Getenv("PATH"),
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
		HasDocker: func(_ context.Context) bool {
			_, err := exec.LookPath("docker")
			return err == nil
		},
		HasImage: func(_ context.Context, image string) bool {
			cmd := exec.Command("docker", "image", "inspect", image)
			return cmd.Run() == nil
		},
	}
}

func KnownTools() []ToolSpec {
	return []ToolSpec{
		{
			Name:        "amass",
			Binary:      "amass",
			GitHubRepo:  "owasp-amass/amass",
			Required:    true,
			Description: "passive subdomain enumeration",
		},
		{
			Name:        "subfinder",
			Binary:      "subfinder",
			GoModule:    "github.com/projectdiscovery/subfinder/v2/cmd/subfinder@latest",
			GitHubRepo:  "projectdiscovery/subfinder",
			Required:    true,
			Description: "passive subdomain enumeration",
		},
		{
			Name:        "chaos",
			Binary:      "chaos",
			GoModule:    "github.com/projectdiscovery/chaos-client/cmd/chaos@latest",
			GitHubRepo:  "projectdiscovery/chaos-client",
			Required:    true,
			Description: "Chaos dataset enumeration",
		},
		{
			Name:        "gau",
			Binary:      "gau",
			GoModule:    "github.com/lc/gau/v2/cmd/gau@latest",
			GitHubRepo:  "lc/gau",
			Required:    true,
			Description: "historical URL collection",
		},
		{
			Name:        "dnsx",
			Binary:      "dnsx",
			GoModule:    "github.com/projectdiscovery/dnsx/cmd/dnsx@latest",
			GitHubRepo:  "projectdiscovery/dnsx",
			Required:    true,
			Description: "DNS resolution",
		},
		{
			Name:        "httpx",
			Binary:      "httpx",
			GoModule:    "github.com/projectdiscovery/httpx/cmd/httpx@latest",
			GitHubRepo:  "projectdiscovery/httpx",
			Required:    true,
			Description: "HTTP probing",
		},
		{
			Name:        "nuclei",
			Binary:      "nuclei",
			GoModule:    "github.com/projectdiscovery/nuclei/v3/cmd/nuclei@latest",
			GitHubRepo:  "projectdiscovery/nuclei",
			Required:    true,
			Description: "template-based scanning",
		},
		{
			Name:        "urlfinder",
			Binary:      "urlfinder",
			GoModule:    "github.com/projectdiscovery/urlfinder/cmd/urlfinder@latest",
			GitHubRepo:  "projectdiscovery/urlfinder",
			Required:    true,
			Description: "passive crawling for URLs",
		},
		{
			Name:        "gotator",
			Binary:      "gotator",
			GoModule:    "github.com/Josue87/gotator@latest",
			GitHubRepo:  "Josue87/gotator",
			Optional:    true,
			Description: "wordlist/generator utility (gotator)",
		},
		{
			Name:        "shuffledns",
			Binary:      "shuffledns",
			GoModule:    "github.com/projectdiscovery/shuffledns/cmd/shuffledns@latest",
			GitHubRepo:  "projectdiscovery/shuffledns",
			Optional:    true,
			Description: "high-parity DNS backend",
		},
		{
			Name:        "massdns",
			Binary:      "massdns",
			Optional:    true,
			Description: "shuffledns dependency",
		},
	}
}

func (t *Toolchain) Find(spec ToolSpec) ToolStatus {
	binary := spec.Binary
	if t.OS == "windows" && !strings.HasSuffix(binary, ".exe") {
		binary += ".exe"
	}

	candidates := []string{}
	if t.BinDir != "" {
		candidates = append(candidates, filepath.Join(t.BinDir, binary))
	}
	candidates = append(candidates, binary)

	for _, candidate := range candidates {
		path, source, err := t.resolveCandidate(candidate)
		if err == nil {
			return ToolStatus{Spec: spec, Path: path, Available: true, Source: source}
		}
	}

	return ToolStatus{Spec: spec}
}

func (t *Toolchain) resolveCandidate(candidate string) (string, string, error) {
	if filepath.IsAbs(candidate) {
		info, err := os.Stat(candidate)
		if err != nil {
			return "", "", err
		}
		if info.IsDir() {
			return "", "", fmt.Errorf("%s is a directory", candidate)
		}
		return candidate, "local-tools", nil
	}

	if strings.Contains(candidate, string(os.PathSeparator)) {
		info, err := os.Stat(candidate)
		if err != nil {
			return "", "", err
		}
		if info.IsDir() {
			return "", "", fmt.Errorf("%s is a directory", candidate)
		}
		return candidate, "local-tools", nil
	}

	path, err := t.LookPath(candidate)
	if err != nil {
		return "", "", err
	}
	return path, "path", nil
}

func (t *Toolchain) Discover() []ToolStatus {
	specs := KnownTools()
	statuses := make([]ToolStatus, 0, len(specs))
	for _, spec := range specs {
		statuses = append(statuses, t.Find(spec))
	}
	return statuses
}

func (t *Toolchain) RequiresSiblingEngine(status ToolStatus) bool {
	return t.OS == "windows" && status.Spec.Name == "amass"
}

func (t *Toolchain) HasSiblingEngine(status ToolStatus) bool {
	if status.Path == "" {
		return false
	}
	engineName := "engine"
	if t.OS == "windows" {
		engineName += ".exe"
	}
	_, err := os.Stat(filepath.Join(filepath.Dir(status.Path), engineName))
	return err == nil
}

func (t *Toolchain) statusByName() map[string]ToolStatus {
	statuses := make(map[string]ToolStatus)
	for _, status := range t.Discover() {
		statuses[status.Spec.Name] = status
	}
	return statuses
}

func (t *Toolchain) ResolveRuntime(ctx context.Context, opts config.CommonOptions) config.RuntimeDecision {
	statuses := t.statusByName()
	highParity := statuses["shuffledns"].Available && statuses["massdns"].Available
	dockerReady := t.HasDocker(ctx) && t.HasImage(ctx, opts.DockerImage)
	requiredNative := t.RequiredToolsAvailable(statuses)

	switch opts.Runtime {
	case config.RuntimeDocker:
		return config.RuntimeDecision{
			Selected:   config.RuntimeDocker,
			Reason:     "requested explicitly",
			HighParity: true,
		}
	case config.RuntimeNative:
		reason := "requested explicitly"
		if !highParity {
			reason = "requested explicitly; using dnsx backend because shuffledns/massdns is unavailable"
		}
		return config.RuntimeDecision{
			Selected:   config.RuntimeNative,
			Reason:     reason,
			HighParity: highParity,
		}
	default:
		if t.OS == "windows" && dockerReady && !highParity {
			return config.RuntimeDecision{
				Selected:   config.RuntimeDocker,
				Reason:     "selected docker for Linux-equivalent parity because shuffledns/massdns is unavailable on native Windows",
				HighParity: true,
			}
		}
		if requiredNative {
			reason := "selected native runtime because all required tools are available"
			if !highParity {
				reason = "selected native runtime with dnsx backend because optional shuffledns/massdns is unavailable"
			}
			return config.RuntimeDecision{
				Selected:   config.RuntimeNative,
				Reason:     reason,
				HighParity: highParity,
			}
		}
		if dockerReady {
			return config.RuntimeDecision{
				Selected:   config.RuntimeDocker,
				Reason:     "selected docker because required native tools are missing",
				HighParity: true,
			}
		}
		return config.RuntimeDecision{
			Selected:   config.RuntimeNative,
			Reason:     "native runtime is the only available option; install missing tools or build the docker image",
			HighParity: highParity,
		}
	}
}

func (t *Toolchain) RequiredToolsAvailable(statuses map[string]ToolStatus) bool {
	for _, spec := range KnownTools() {
		if !spec.Required {
			continue
		}
		status := statuses[spec.Name]
		if spec.Name == "amass" && t.OS == "windows" {
			continue
		}
		if !status.Available {
			return false
		}
	}
	return true
}

func (t *Toolchain) BuildDoctorReport(ctx context.Context, opts config.CommonOptions) DoctorReport {
	statuses := t.Discover()
	statusMap := make(map[string]ToolStatus, len(statuses))
	report := DoctorReport{
		Statuses:        statuses,
		DockerAvailable: t.HasDocker(ctx),
		ImageAvailable:  t.HasDocker(ctx) && t.HasImage(ctx, opts.DockerImage),
	}
	report.Runtime = t.ResolveRuntime(ctx, opts)

	for _, status := range statuses {
		statusMap[status.Spec.Name] = status
		isRequired := status.Spec.Required
		if status.Spec.Name == "amass" && t.OS == "windows" {
			isRequired = false
		}
		if isRequired && !status.Available && report.Runtime.Selected != config.RuntimeDocker {
			report.Errors = append(report.Errors, fmt.Sprintf("missing required tool: %s", status.Spec.Name))
		}
	}

	if strings.TrimSpace(opts.ChaosAPIKey) == "" {
		report.Warnings = append(report.Warnings, "CHAOS_API_KEY (or PDCP_API_KEY) is not set; the Chaos stage will be skipped")
	}
	if strings.TrimSpace(opts.ResolversPath) == "" {
		report.Errors = append(report.Errors, "resolver list path is empty")
	} else if _, err := os.Stat(opts.ResolversPath); err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("resolver list not found: %s", opts.ResolversPath))
	}
	if strings.TrimSpace(opts.SubfinderProviderConfig) != "" {
		if _, err := os.Stat(opts.SubfinderProviderConfig); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("subfinder provider config not found: %s", opts.SubfinderProviderConfig))
		}
	}
	if strings.TrimSpace(opts.AmassConfig) != "" {
		if _, err := os.Stat(opts.AmassConfig); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("amass config not found: %s", opts.AmassConfig))
		}
	}
	if report.Runtime.Selected == config.RuntimeDocker && !report.DockerAvailable {
		report.Errors = append(report.Errors, "docker runtime selected but docker is not installed")
	}
	if report.Runtime.Selected == config.RuntimeDocker && !report.ImageAvailable {
		report.Errors = append(report.Errors, fmt.Sprintf("docker runtime selected but image %s is not available", opts.DockerImage))
	}
	if !statusMap["shuffledns"].Available || !statusMap["massdns"].Available {
		report.Warnings = append(report.Warnings, "high-parity shuffledns/massdns backend is unavailable; dnsx will be used for native runs")
	}
	if amassStatus, ok := statusMap["amass"]; ok && amassStatus.Available && t.RequiresSiblingEngine(amassStatus) && !t.HasSiblingEngine(amassStatus) {
		report.Warnings = append(report.Warnings, "amass.exe is installed but engine.exe is missing; native Windows runs will skip the amass stage")
	}

	return report
}
