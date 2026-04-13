package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/alpernae/recon/internal/config"
	"github.com/alpernae/recon/internal/install"
	"github.com/alpernae/recon/internal/pipeline"
	"github.com/alpernae/recon/internal/toolchain"
	"github.com/alpernae/recon/internal/util"
)

type CLI struct {
	stdout io.Writer
	stderr io.Writer
}

func NewCLI(stdout, stderr io.Writer) *CLI {
	return &CLI{stdout: stdout, stderr: stderr}
}

func (c *CLI) Run(ctx context.Context, args []string) int {
	if len(args) == 0 {
		c.printUsage()
		return 2
	}

	switch args[0] {
	case "doctor":
		if err := c.runDoctor(ctx, args[1:]); err != nil {
			fmt.Fprintln(c.stderr, "Error:", err)
			return 1
		}
		return 0
	case "install-tools":
		if err := c.runInstallTools(ctx, args[1:]); err != nil {
			fmt.Fprintln(c.stderr, "Error:", err)
			return 1
		}
		return 0
	case "help", "-h", "--help":
		c.printUsage()
		return 0
	default:
		if err := c.runRecon(ctx, args); err != nil {
			fmt.Fprintln(c.stderr, "Error:", err)
			return 1
		}
		return 0
	}
}

func (c *CLI) runRecon(ctx context.Context, args []string) error {
	domain := strings.TrimSpace(args[0])
	if domain == "" {
		return errors.New("domain is required")
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	fs := c.newFlagSet("recon")
	runtimeFlag := fs.String("runtime", "auto", "runtime to use: auto, native, docker")
	outDir := fs.String("outdir", "", "output directory")
	resolvers := fs.String("resolvers", envOrDefault("RECON_RESOLVERS", config.DefaultResolversPath(cwd)), "resolver list path")
	chaosAPIKey := fs.String("chaos-api-key", chaosAPIKeyFromEnv(), "Chaos API key")
	subfinderConfig := fs.String("subfinder-provider-config", os.Getenv("SUBFINDER_PROVIDER_CONFIG"), "subfinder provider config path")
	amassConfig := fs.String("amass-config", os.Getenv("AMASS_CONFIG"), "amass config path")
	dockerImage := fs.String("docker-image", envOrDefault("RECON_DOCKER_IMAGE", config.DefaultDockerImage), "docker image for docker runtime")
	toolRoot := fs.String("tools-dir", config.DefaultToolRoot(cwd), "local tools directory")
	fast := fs.Bool("fast", false, "reduce concurrency and permutation depth")
	deep := fs.Bool("deep", false, "increase concurrency and permutation depth")
	verbose := fs.Bool("verbose", false, "enable verbose output")
	color := fs.Bool("color", true, "enable colored output in terminal")
	screenshot := fs.Bool("screenshot", false, "capture screenshots of live hosts")
	screenshotDir := fs.String("screenshot-dir", "", "directory to save screenshots (defaults to <outdir>/screenshots)")
	installChrome := fs.Bool("install-chrome", false, "install headless Chrome/Chromium before running (may require admin privileges)")

	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	mode, err := config.ParseMode(*fast, *deep)
	if err != nil {
		return err
	}
	runtimeMode, err := config.ParseRuntime(*runtimeFlag)
	if err != nil {
		return err
	}

	threads, rate := config.ModeProfile(mode)
	common := config.CommonOptions{
		CWD:                     cwd,
		ToolRoot:                *toolRoot,
		Runtime:                 runtimeMode,
		ResolversPath:           resolvePath(cwd, *resolvers),
		ChaosAPIKey:             strings.TrimSpace(*chaosAPIKey),
		SubfinderProviderConfig: resolveOptionalPath(cwd, *subfinderConfig),
		AmassConfig:             resolveOptionalPath(cwd, *amassConfig),
		DockerImage:             strings.TrimSpace(*dockerImage),
	}

	opts := config.RunOptions{
		CommonOptions: common,
		Domain:        strings.ToLower(domain),
		Mode:          mode,
		OutDir:        config.ResolveOutDir(cwd, domain, *outDir),
		Threads:       threads,
		Rate:          rate,
		Verbose:       *verbose,
		Color:         *color,
		Screenshot:    *screenshot,
		ScreenshotDir: strings.TrimSpace(*screenshotDir),
	}

	if *installChrome {
		fmt.Fprintf(c.stdout, "[*] Installing headless Chrome/Chromium...\n")
		if err := install.InstallChrome(ctx); err != nil {
			return fmt.Errorf("install chrome: %w", err)
		}
	}

	tc := toolchain.New(config.ToolBinDir(common.ToolRoot))
	decision := tc.ResolveRuntime(ctx, common)
	fmt.Fprintf(c.stdout, "[*] Runtime selection: %s (%s)\n", decision.Selected, decision.Reason)

	if decision.Selected == config.RuntimeDocker && os.Getenv("RECON_IN_DOCKER") != "1" {
		return c.delegateToDocker(ctx, opts)
	}

	report := tc.BuildDoctorReport(ctx, common)
	if err := validateReportForRun(report); err != nil {
		return err
	}
	if err := util.EnsureWritableDir(opts.OutDir); err != nil {
		return fmt.Errorf("output directory is not writable: %w", err)
	}

	pipe := pipeline.New(tc, c.stdout, c.stderr)
	return pipe.RunAsync(ctx, opts, decision)
}

func (c *CLI) runDoctor(ctx context.Context, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	fs := c.newFlagSet("doctor")
	runtimeFlag := fs.String("runtime", "auto", "runtime to validate: auto, native, docker")
	outDir := fs.String("outdir", "", "directory to test for writes")
	resolvers := fs.String("resolvers", envOrDefault("RECON_RESOLVERS", config.DefaultResolversPath(cwd)), "resolver list path")
	chaosAPIKey := fs.String("chaos-api-key", chaosAPIKeyFromEnv(), "Chaos API key")
	subfinderConfig := fs.String("subfinder-provider-config", os.Getenv("SUBFINDER_PROVIDER_CONFIG"), "subfinder provider config path")
	amassConfig := fs.String("amass-config", os.Getenv("AMASS_CONFIG"), "amass config path")
	dockerImage := fs.String("docker-image", envOrDefault("RECON_DOCKER_IMAGE", config.DefaultDockerImage), "docker image for docker runtime")
	toolRoot := fs.String("tools-dir", config.DefaultToolRoot(cwd), "local tools directory")

	if err := fs.Parse(args); err != nil {
		return err
	}

	runtimeMode, err := config.ParseRuntime(*runtimeFlag)
	if err != nil {
		return err
	}

	common := config.CommonOptions{
		CWD:                     cwd,
		ToolRoot:                *toolRoot,
		Runtime:                 runtimeMode,
		ResolversPath:           resolvePath(cwd, *resolvers),
		ChaosAPIKey:             strings.TrimSpace(*chaosAPIKey),
		SubfinderProviderConfig: resolveOptionalPath(cwd, *subfinderConfig),
		AmassConfig:             resolveOptionalPath(cwd, *amassConfig),
		DockerImage:             strings.TrimSpace(*dockerImage),
	}
	doctorOpts := config.DoctorOptions{
		CommonOptions: common,
		OutDir:        resolveOptionalPath(cwd, *outDir),
	}

	tc := toolchain.New(config.ToolBinDir(common.ToolRoot))
	report := tc.BuildDoctorReport(ctx, common)
	c.printDoctorReport(report, doctorOpts.OutDir, cwd)

	writeTarget := cwd
	if doctorOpts.OutDir != "" {
		writeTarget = doctorOpts.OutDir
	}
	if err := util.EnsureWritableDir(writeTarget); err != nil {
		return fmt.Errorf("write check failed for %s: %w", writeTarget, err)
	}

	if len(report.Errors) > 0 {
		return fmt.Errorf("doctor found %d blocking issue(s)", len(report.Errors))
	}
	return nil
}

func (c *CLI) runInstallTools(ctx context.Context, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	fs := c.newFlagSet("install-tools")
	toolRoot := fs.String("tools-dir", config.DefaultToolRoot(cwd), "local tools directory (workspace .tools/<os>-<arch> by default)")
	includeOptional := fs.Bool("include-optional", true, "install optional tools when supported")
	force := fs.Bool("force", false, "reinstall tools even if they already exist locally")
	targetOS := fs.String("target-os", runtime.GOOS, "target operating system")
	targetArch := fs.String("target-arch", runtime.GOARCH, "target architecture")
	global := fs.Bool("global", false, "install globally to user's Go bin (GOBIN or GOPATH/bin); falls back to PATH writable dir")
	remove := fs.Bool("remove", false, "remove installed tools from the target directory")
	dockerImage := fs.String("docker-image", envOrDefault("RECON_DOCKER_IMAGE", config.DefaultDockerImage), "docker image for docker runtime")

	if err := fs.Parse(args); err != nil {
		return err
	}

	opts := config.InstallOptions{
		CommonOptions: config.CommonOptions{
			CWD:         cwd,
			ToolRoot:    *toolRoot,
			Runtime:     config.RuntimeNative,
			DockerImage: strings.TrimSpace(*dockerImage),
		},
		IncludeOptional: *includeOptional,
		Force:           *force,
		TargetOS:        strings.TrimSpace(*targetOS),
		TargetArch:      strings.TrimSpace(*targetArch),
		Global:          *global,
	}

	// Determine effective bin directory.
	// - If --global is requested, prefer `GOBIN` (go env GOBIN), then `GOPATH/bin`,
	//   then fall back to the first writable directory in PATH.
	// - Otherwise use the configured tools directory (workspace `.tools/...`).
	var binDir string
	if *global {
		// Try to use `go env GOBIN` if available
		gobin := ""
		if path, err := exec.LookPath("go"); err == nil && path != "" {
			// try GOBIN
			if out, err := exec.CommandContext(ctx, "go", "env", "GOBIN").Output(); err == nil {
				gobin = strings.TrimSpace(string(out))
			}
			if gobin == "" {
				// fallback to GOPATH/bin
				if out, err := exec.CommandContext(ctx, "go", "env", "GOPATH").Output(); err == nil {
					gopath := strings.TrimSpace(string(out))
					if gopath != "" {
						gobin = filepath.Join(gopath, "bin")
					}
				}
			}
		}
		if gobin != "" {
			binDir = gobin
		} else {
			dir, err := util.FindWritableDirInPATH()
			if err != nil {
				return fmt.Errorf("cannot install globally: %w", err)
			}
			binDir = dir
		}
	} else {
		if strings.TrimSpace(*toolRoot) == "" {
			binDir = config.ToolBinDir(config.DefaultToolRoot(cwd))
		} else {
			binDir = config.ToolBinDir(*toolRoot)
		}
	}

	tc := toolchain.New(binDir)
	inst := install.New(tc, c.stdout, c.stderr)

	if *remove {
		if err := inst.Uninstall(ctx, opts); err != nil {
			return err
		}
		fmt.Fprintf(c.stdout, "[✓] Tools removed from %s\n", binDir)
		return nil
	}

	if err := inst.Install(ctx, opts); err != nil {
		return err
	}
	fmt.Fprintf(c.stdout, "[✓] Tools installed under %s\n", binDir)
	return nil
}

func (c *CLI) delegateToDocker(ctx context.Context, opts config.RunOptions) error {
	if opts.DockerImage == "" {
		return errors.New("docker runtime requires --docker-image")
	}

	containerOutDir, err := toContainerPath(opts.CWD, opts.OutDir)
	if err != nil {
		return err
	}
	containerResolvers, err := toContainerPath(opts.CWD, opts.ResolversPath)
	if err != nil {
		return err
	}

	args := []string{
		"run",
		"--rm",
		"-e", "RECON_IN_DOCKER=1",
		"-v", opts.CWD + ":/workspace",
		"-w", "/workspace",
	}
	if opts.ChaosAPIKey != "" {
		args = append(args, "-e", "CHAOS_API_KEY="+opts.ChaosAPIKey)
	}
	args = append(args, opts.DockerImage, opts.Domain, "--runtime", "native", "--outdir", containerOutDir, "--resolvers", containerResolvers)
	switch opts.Mode {
	case config.ModeFast:
		args = append(args, "--fast")
	case config.ModeDeep:
		args = append(args, "--deep")
	}
	if opts.SubfinderProviderConfig != "" {
		containerPath, err := toContainerPath(opts.CWD, opts.SubfinderProviderConfig)
		if err != nil {
			return err
		}
		args = append(args, "--subfinder-provider-config", containerPath)
	}
	if opts.AmassConfig != "" {
		containerPath, err := toContainerPath(opts.CWD, opts.AmassConfig)
		if err != nil {
			return err
		}
		args = append(args, "--amass-config", containerPath)
	}

	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout = c.stdout
	cmd.Stderr = c.stderr
	return cmd.Run()
}

func validateReportForRun(report toolchain.DoctorReport) error {
	if report.Runtime.Selected == config.RuntimeDocker {
		for _, issue := range report.Errors {
			if strings.HasPrefix(issue, "missing required tool:") {
				continue
			}
			return errors.New(issue)
		}
		return nil
	}
	if len(report.Errors) == 0 {
		return nil
	}
	return errors.New(strings.Join(report.Errors, "; "))
}

func (c *CLI) printDoctorReport(report toolchain.DoctorReport, outDir, cwd string) {
	fmt.Fprintf(c.stdout, "Runtime: %s\n", report.Runtime.Selected)
	fmt.Fprintf(c.stdout, "Reason: %s\n", report.Runtime.Reason)
	fmt.Fprintf(c.stdout, "Docker available: %t\n", report.DockerAvailable)
	fmt.Fprintf(c.stdout, "Docker image available: %t\n", report.ImageAvailable)
	writeTarget := cwd
	if outDir != "" {
		writeTarget = outDir
	}
	fmt.Fprintf(c.stdout, "Write check: %s\n", writeTarget)
	fmt.Fprintln(c.stdout, "Tools:")
	for _, status := range report.Statuses {
		state := "missing"
		if status.Available {
			state = "ok"
		}
		fmt.Fprintf(c.stdout, "  - %s: %s", status.Spec.Name, state)
		if status.Path != "" {
			fmt.Fprintf(c.stdout, " (%s)", status.Path)
		}
		fmt.Fprintln(c.stdout)
	}
	if len(report.Warnings) > 0 {
		fmt.Fprintln(c.stdout, "Warnings:")
		for _, warning := range report.Warnings {
			fmt.Fprintf(c.stdout, "  - %s\n", warning)
		}
	}
	if len(report.Errors) > 0 {
		fmt.Fprintln(c.stdout, "Errors:")
		for _, issue := range report.Errors {
			fmt.Fprintf(c.stdout, "  - %s\n", issue)
		}
	}
}

func (c *CLI) printUsage() {
	fmt.Fprintln(c.stdout, "Usage:")
	fmt.Fprintln(c.stdout, "  recon <domain> [--fast|--deep] [--runtime auto|native|docker] [--outdir path]")
	fmt.Fprintln(c.stdout, "  recon doctor [--runtime auto|native|docker]")
	fmt.Fprintln(c.stdout, "  recon install-tools [--tools-dir path]")
}

func (c *CLI) newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(c.stderr)
	return fs
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func chaosAPIKeyFromEnv() string {
	if value := strings.TrimSpace(os.Getenv("CHAOS_API_KEY")); value != "" {
		return value
	}
	return strings.TrimSpace(os.Getenv("PDCP_API_KEY"))
}

func resolvePath(cwd, value string) string {
	if filepath.IsAbs(value) {
		return value
	}
	return filepath.Join(cwd, value)
}

func resolveOptionalPath(cwd, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return resolvePath(cwd, value)
}

func toContainerPath(cwd, hostPath string) (string, error) {
	abs := hostPath
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(cwd, hostPath)
	}
	if !util.PathWithin(cwd, abs) {
		return "", fmt.Errorf("docker runtime only supports paths inside %s: %s", cwd, hostPath)
	}
	rel, err := filepath.Rel(cwd, abs)
	if err != nil {
		return "", err
	}
	if rel == "." {
		return "/workspace", nil
	}
	return "/workspace/" + filepath.ToSlash(rel), nil
}
