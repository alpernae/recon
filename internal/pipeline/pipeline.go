package pipeline

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alpernae/recon/internal/config"
	"github.com/alpernae/recon/internal/install"
	"github.com/alpernae/recon/internal/toolchain"
	"github.com/alpernae/recon/internal/util"
)

type Pipeline struct {
	Tools  *toolchain.Toolchain
	Stdout io.Writer
	Stderr io.Writer
}

// LiveHost holds a small summary about an HTTP probe result.
type LiveHost struct {
	Host   string
	URL    string
	Status int
	Title  string
	Techs  []string
}

func New(tools *toolchain.Toolchain, stdout, stderr io.Writer) *Pipeline {
	return &Pipeline{
		Tools:  tools,
		Stdout: stdout,
		Stderr: stderr,
	}
}

func (p *Pipeline) Run(ctx context.Context, opts config.RunOptions, decision config.RuntimeDecision) error {
	if err := util.EnsureWritableDir(opts.OutDir); err != nil {
		return fmt.Errorf("prepare output directory: %w", err)
	}

	for _, dir := range []string{
		filepath.Join(opts.OutDir, "passive"),
		filepath.Join(opts.OutDir, "resolved"),
		filepath.Join(opts.OutDir, "perms"),
		filepath.Join(opts.OutDir, "intel"),
		filepath.Join(opts.OutDir, "live"),
		filepath.Join(opts.OutDir, "scan"),
	} {
		if err := util.EnsureDir(dir); err != nil {
			return err
		}
	}

	// Check for missing tools and Chrome, and auto-install when needed.
	// Default behavior: on each run, if required tools are missing (for native runtime),
	// install them. Also install headless Chrome/Chromium if screenshots are requested
	// and Chrome is absent. Set `RECON_AUTO_INSTALL_TOOLS_INCLUDE_OPTIONAL=1` to include
	// optional tools in the installation.
	if decision.Selected == config.RuntimeNative {
		statuses := p.Tools.Discover()
		statusMap := make(map[string]toolchain.ToolStatus, len(statuses))
		for _, s := range statuses {
			statusMap[s.Spec.Name] = s
		}

		// detect missing required tools
		missingRequired := false
		for _, spec := range toolchain.KnownTools() {
			if !spec.Required {
				continue
			}
			if spec.Name == "amass" && p.Tools.OS == "windows" {
				continue
			}
			status := statusMap[spec.Name]
			if !status.Available {
				missingRequired = true
				break
			}
		}

		// detect missing chrome when screenshots are enabled
		chromeMissing := false
		if opts.Screenshot {
			if _, err := findChromeExec(); err != nil {
				chromeMissing = true
			}
		}

		if missingRequired || chromeMissing {
			includeOptional := false
			if opt := strings.TrimSpace(os.Getenv("RECON_AUTO_INSTALL_TOOLS_INCLUDE_OPTIONAL")); opt != "" {
				o := strings.ToLower(opt)
				includeOptional = (o == "1" || o == "true" || o == "yes")
			}
			p.logf("[+] Missing required tools or Chrome detected; installing (include-optional=%v)\n", includeOptional)

			inst := install.New(p.Tools, p.Stdout, p.Stderr)
			instOpts := config.InstallOptions{
				CommonOptions:   opts.CommonOptions,
				IncludeOptional: includeOptional,
				Force:           false,
				TargetOS:        runtime.GOOS,
				TargetArch:      runtime.GOARCH,
				Global:          false,
			}

			if missingRequired {
				if err := inst.Install(ctx, instOpts); err != nil {
					return fmt.Errorf("auto-install tools failed: %w", err)
				}
			}

			if chromeMissing {
				if err := install.InstallChrome(ctx); err != nil {
					return fmt.Errorf("auto-install chrome failed: %w", err)
				}
			}
		}
	}

	p.logf("[+] Starting recon on %s (mode: %s, runtime: %s)\n", opts.Domain, opts.Mode, decision.Selected)

	passiveAll, err := p.runEnum(ctx, opts)
	if err != nil {
		return err
	}

	passiveFinal, intelTokens, err := p.runIntel(ctx, opts, passiveAll)
	if err != nil {
		return err
	}

	permutations, err := p.runPermute(opts, passiveFinal, intelTokens)
	if err != nil {
		return err
	}

	resolved, err := p.runResolve(ctx, opts, decision, passiveFinal, permutations)
	if err != nil {
		return err
	}

	liveHosts, err := p.runProbe(ctx, opts, resolved)
	if err != nil {
		return err
	}

	if err := p.runScan(ctx, opts, liveHosts); err != nil {
		return err
	}

	p.logf("[✓] Recon complete -> %s\n", opts.OutDir)
	return nil
}

func (p *Pipeline) runEnum(ctx context.Context, opts config.RunOptions) ([]string, error) {
	p.logf("[+] Passive enum...\n")

	passiveDir := filepath.Join(opts.OutDir, "passive")
	amassOutput := filepath.Join(passiveDir, "amass.txt")
	subfinderOutput := filepath.Join(passiveDir, "subfinder.txt")
	chaosOutput := filepath.Join(passiveDir, "chaos.txt")
	allOutput := filepath.Join(passiveDir, "all.txt")

	var mu sync.Mutex
	var warnings []string
	successCount := 0
	var wg sync.WaitGroup

	recordSuccess := func() {
		mu.Lock()
		successCount++
		mu.Unlock()
	}

	recordWarning := func(message string) {
		mu.Lock()
		warnings = append(warnings, message)
		mu.Unlock()
	}

	run := func(source string, fn func() error) {
		defer wg.Done()
		if err := fn(); err != nil {
			recordWarning(fmt.Sprintf("%s failed and will be skipped: %v", source, err))
			return
		}
		recordSuccess()
	}

	amassStatus := p.Tools.Find(toolchain.ToolSpec{Name: "amass", Binary: "amass"})
	if p.shouldRunAmass(amassStatus) {
		wg.Add(1)
		go run("amass", func() error {
			args := []string{"enum", "-passive", "-d", opts.Domain, "-o", amassOutput}
			if strings.TrimSpace(opts.AmassConfig) != "" {
				args = append(args, "-config", opts.AmassConfig)
			}
			return p.runTool(ctx, "amass", args, nil)
		})
	} else {
		recordWarning("amass is being skipped on native Windows because engine.exe is missing")
		if err := util.WriteStringAtomic(amassOutput, "", false); err != nil {
			return nil, err
		}
	}

	wg.Add(1)
	go run("subfinder", func() error {
		args := []string{"-d", opts.Domain, "-silent", "-o", subfinderOutput}
		if strings.TrimSpace(opts.SubfinderProviderConfig) != "" {
			args = append(args, "-pc", opts.SubfinderProviderConfig)
		}
		return p.runTool(ctx, "subfinder", args, nil)
	})

	if strings.TrimSpace(opts.ChaosAPIKey) != "" {
		wg.Add(1)
		go run("chaos", func() error {
			args := []string{"-d", opts.Domain, "-silent", "-o", chaosOutput, "-key", opts.ChaosAPIKey}
			return p.runTool(ctx, "chaos", args, nil)
		})
	} else if err := util.WriteStringAtomic(chaosOutput, "", false); err != nil {
		return nil, err
	}

	wg.Wait()
	if successCount == 0 {
		return nil, fmt.Errorf("all passive enumeration sources failed")
	}
	for _, warning := range warnings {
		p.logf("[!] %s\n", warning)
	}

	amassLines, err := util.ReadLines(amassOutput)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	subfinderLines, err := util.ReadLines(subfinderOutput)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	chaosLines, err := util.ReadLines(chaosOutput)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	all := util.UniqueSortedLower(append(append(amassLines, subfinderLines...), chaosLines...))
	if err := util.WriteTextLinesAtomic(allOutput, all); err != nil {
		return nil, err
	}
	return all, nil
}

func (p *Pipeline) shouldRunAmass(status toolchain.ToolStatus) bool {
	if !status.Available {
		return false
	}
	if p.Tools.RequiresSiblingEngine(status) && !p.Tools.HasSiblingEngine(status) {
		return false
	}
	return true
}

func (p *Pipeline) runIntel(ctx context.Context, opts config.RunOptions, passiveAll []string) ([]string, []string, error) {
	p.logf("[+] Collecting URLs and extracting intel...\n")

	intelDir := filepath.Join(opts.OutDir, "intel")
	passiveDir := filepath.Join(opts.OutDir, "passive")
	urlsPath := filepath.Join(intelDir, "urls.txt")
	tokensPath := filepath.Join(intelDir, "tokens.txt")
	finalPassivePath := filepath.Join(passiveDir, "final.txt")

	stdout, err := p.runToolCapture(ctx, "gau", []string{opts.Domain}, nil)
	if err != nil {
		return nil, nil, err
	}

	urlLines := util.UniqueSorted(strings.Split(strings.TrimSpace(stdout), "\n"))
	if err := util.WriteTextLinesAtomic(urlsPath, urlLines); err != nil {
		return nil, nil, err
	}

	tokens, archivedHosts := ExtractIntel(opts.Domain, urlLines)
	if err := util.WriteTextLinesAtomic(tokensPath, tokens); err != nil {
		return nil, nil, err
	}

	finalPassive := util.UniqueSortedLower(append(passiveAll, archivedHosts...))
	if err := util.WriteTextLinesAtomic(finalPassivePath, finalPassive); err != nil {
		return nil, nil, err
	}

	return finalPassive, tokens, nil
}

func (p *Pipeline) runPermute(opts config.RunOptions, passiveFinal, intelTokens []string) ([]string, error) {
	p.logf("[+] Generating permutations...\n")

	baseTokens, err := util.ReadLines(filepath.Join(opts.CWD, "wordlists", "base_tokens.txt"))
	if err != nil {
		return nil, fmt.Errorf("read base tokens: %w", err)
	}

	permutations := GeneratePermutations(opts.Domain, passiveFinal, intelTokens, baseTokens, opts.Mode)
	if err := util.WriteTextLinesAtomic(filepath.Join(opts.OutDir, "perms", "all.txt"), permutations); err != nil {
		return nil, err
	}

	return permutations, nil
}

func (p *Pipeline) runResolve(ctx context.Context, opts config.RunOptions, decision config.RuntimeDecision, passiveFinal, permutations []string) ([]string, error) {
	resolveDir := filepath.Join(opts.OutDir, "resolved")
	p.logf("[+] Resolving subdomains...\n")

	statusMap := make(map[string]toolchain.ToolStatus)
	for _, status := range p.Tools.Discover() {
		statusMap[status.Spec.Name] = status
	}

	backend := "dnsx"
	if decision.HighParity && statusMap["shuffledns"].Available && statusMap["massdns"].Available {
		backend = "shuffledns"
	}

	passiveResolved, err := p.resolveHosts(ctx, opts, backend, passiveFinal, statusMap)
	if err != nil {
		return nil, err
	}
	permutationResolved, err := p.resolveHosts(ctx, opts, backend, permutations, statusMap)
	if err != nil {
		return nil, err
	}

	if err := util.WriteTextLinesAtomic(filepath.Join(resolveDir, "passive.txt"), passiveResolved); err != nil {
		return nil, err
	}
	if err := util.WriteTextLinesAtomic(filepath.Join(resolveDir, "perms.txt"), permutationResolved); err != nil {
		return nil, err
	}

	all := util.UniqueSortedLower(append(passiveResolved, permutationResolved...))
	if err := util.WriteTextLinesAtomic(filepath.Join(resolveDir, "all.txt"), all); err != nil {
		return nil, err
	}
	if err := util.WriteTextLinesAtomic(filepath.Join(resolveDir, "clean.txt"), all); err != nil {
		return nil, err
	}
	return all, nil
}

func (p *Pipeline) runProbe(ctx context.Context, opts config.RunOptions, resolved []string) ([]string, error) {
	p.logf("[+] Probing live hosts...\n")

	liveDir := filepath.Join(opts.OutDir, "live")
	inputPath := filepath.Join(opts.OutDir, "resolved", "clean.txt")
	outputPath := filepath.Join(liveDir, "live.txt")
	jsonPath := filepath.Join(liveDir, "live.json")

	if len(resolved) == 0 {
		if err := util.WriteStringAtomic(outputPath, "", false); err != nil {
			return nil, err
		}
		if err := util.WriteStringAtomic(jsonPath, "", false); err != nil {
			return nil, err
		}
		return nil, nil
	}

	args := []string{
		"-l", inputPath,
		"-silent",
		"-threads", strconv.Itoa(opts.Threads),
		"-rate-limit", strconv.Itoa(opts.Rate),
		"-mc", "200,301,302,403",
		"-json",
		"-title",
		"-status-code",
		"-tech-detect",
	}

	stdout, err := p.runToolCapture(ctx, "httpx", args, nil)
	if err != nil {
		return nil, err
	}

	// Save raw json output (may be many lines)
	if err := util.WriteStringAtomic(jsonPath, stdout, false); err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(strings.NewReader(stdout))
	entries := make([]LiveHost, 0)
	seen := make(map[string]struct{})
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			// fallback to line-based host
			host := normalizeResolvedLine(line)
			if host == "" {
				continue
			}
			if _, ok := seen[host]; ok {
				continue
			}
			seen[host] = struct{}{}
			entries = append(entries, LiveHost{Host: host})
			continue
		}

		host := ""
		if v, ok := m["host"].(string); ok && v != "" {
			host = v
		}
		urlStr := ""
		if v, ok := m["url"].(string); ok && v != "" {
			urlStr = v
		}
		status := 0
		if v, ok := m["status_code"].(float64); ok {
			status = int(v)
		} else if v, ok := m["status"].(float64); ok {
			status = int(v)
		}
		title := ""
		if v, ok := m["title"].(string); ok {
			title = v
		}
		var techs []string
		if v, ok := m["technologies"].([]interface{}); ok {
			for _, t := range v {
				if s, ok := t.(string); ok {
					techs = append(techs, s)
				}
			}
		} else if v, ok := m["tech"].([]interface{}); ok {
			for _, t := range v {
				if s, ok := t.(string); ok {
					techs = append(techs, s)
				}
			}
		}

		if host == "" && urlStr != "" {
			if u, err := url.Parse(urlStr); err == nil {
				host = u.Hostname()
			}
		}
		host = strings.ToLower(strings.TrimSpace(host))
		if host == "" {
			continue
		}
		if _, ok := seen[host]; ok {
			continue
		}
		seen[host] = struct{}{}
		entries = append(entries, LiveHost{Host: host, URL: urlStr, Status: status, Title: title, Techs: techs})
	}

	// write simple host list
	outLines := make([]string, 0, len(entries))
	for _, e := range entries {
		outLines = append(outLines, e.Host)
	}
	if err := util.WriteTextLinesAtomic(outputPath, outLines); err != nil {
		return nil, err
	}

	// Pretty-print results to stdout
	color := opts.Color
	green := ""
	cyan := ""
	yellow := ""
	bold := ""
	reset := ""
	if color {
		green = "\x1b[32m"
		cyan = "\x1b[36m"
		yellow = "\x1b[33m"
		bold = "\x1b[1m"
		reset = "\x1b[0m"
	}

	for _, e := range entries {
		if e.Status != 0 {
			p.logf("%s[LIVE]%s %s%s%s %s%d%s %s\n", green, reset, bold, e.Host, reset, cyan, e.Status, reset, e.URL)
		} else {
			p.logf("%s[LIVE]%s %s%s%s %s\n", green, reset, bold, e.Host, reset, e.URL)
		}
		if opts.Verbose {
			if e.Title != "" {
				p.logf("  %sTitle:%s %s\n", yellow, reset, e.Title)
			}
			if len(e.Techs) > 0 {
				p.logf("  %sTechs:%s %s\n", yellow, reset, strings.Join(e.Techs, ", "))
			}
		}
	}

	// Screenshots (concurrent, limited)
	if opts.Screenshot {
		screenshotDir := opts.ScreenshotDir
		if strings.TrimSpace(screenshotDir) == "" {
			screenshotDir = filepath.Join(opts.OutDir, "screenshots")
		}
		if err := util.EnsureDir(screenshotDir); err != nil {
			p.logf("[!] cannot create screenshot dir: %v\n", err)
		} else {
			concurrency := opts.Threads / 50
			if concurrency < 1 {
				concurrency = 1
			}
			if concurrency > 8 {
				concurrency = 8
			}
			// spawn workers
			jobs := make(chan LiveHost)
			var wg sync.WaitGroup
			for i := 0; i < concurrency; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for host := range jobs {
						// build output path
						outFile := filepath.Join(screenshotDir, host.Host+".png")
						if host.URL == "" {
							host.URL = "https://" + host.Host
						}
						if err := CaptureScreenshot(ctx, host.URL, outFile); err != nil {
							p.logf("[!] screenshot %s: %v\n", host.Host, err)
						} else {
							p.logf("[✓] screenshot: %s -> %s\n", host.Host, outFile)
						}
						// small delay to avoid thundering
						time.Sleep(200 * time.Millisecond)
					}
				}()
			}
			for _, e := range entries {
				jobs <- e
			}
			close(jobs)
			wg.Wait()
		}
	}

	return outLines, nil
}

func (p *Pipeline) runScan(ctx context.Context, opts config.RunOptions, liveHosts []string) error {
	p.logf("[+] Running nuclei...\n")

	outputPath := filepath.Join(opts.OutDir, "scan", "nuclei.txt")
	if len(liveHosts) == 0 {
		return util.WriteStringAtomic(outputPath, "", false)
	}

	args := []string{
		"-l", filepath.Join(opts.OutDir, "live", "live.txt"),
		"-severity", "low,medium,high,critical",
		"-silent",
		"-o", outputPath,
	}
	return p.runTool(ctx, "nuclei", args, nil)
}

func (p *Pipeline) resolveHosts(ctx context.Context, opts config.RunOptions, backend string, hosts []string, statuses map[string]toolchain.ToolStatus) ([]string, error) {
	hosts = util.UniqueSortedLower(hosts)
	if len(hosts) == 0 {
		return nil, nil
	}

	chunkSize := 5000
	switch opts.Mode {
	case config.ModeFast:
		chunkSize = 2000
	case config.ModeDeep:
		chunkSize = 10000
	}

	workers := opts.Threads / 50
	if workers < 2 {
		workers = 2
	}
	if workers > 8 {
		workers = 8
	}

	tempDir := filepath.Join(opts.OutDir, "resolved", ".chunks")
	if err := util.EnsureDir(tempDir); err != nil {
		return nil, err
	}
	defer os.RemoveAll(tempDir)

	chunks := util.ChunkStrings(hosts, chunkSize)
	type result struct {
		lines []string
		err   error
	}

	jobs := make(chan []string)
	results := make(chan result, len(chunks))
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			for chunk := range jobs {
				lines, err := p.resolveChunk(ctx, opts, backend, statuses, tempDir, index, chunk)
				results <- result{lines: lines, err: err}
			}
		}(i)
	}

	go func() {
		for _, chunk := range chunks {
			jobs <- chunk
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	var merged []string
	for result := range results {
		if result.err != nil {
			return nil, result.err
		}
		merged = append(merged, result.lines...)
	}

	return util.UniqueSortedLower(merged), nil
}

func (p *Pipeline) resolveChunk(ctx context.Context, opts config.RunOptions, backend string, statuses map[string]toolchain.ToolStatus, tempDir string, workerID int, chunk []string) ([]string, error) {
	tmpFile, err := os.CreateTemp(tempDir, fmt.Sprintf("chunk-%d-*.txt", workerID))
	if err != nil {
		return nil, err
	}
	inputPath := tmpFile.Name()
	if err := tmpFile.Close(); err != nil {
		return nil, err
	}
	defer os.Remove(inputPath)

	if err := util.WriteTextLinesAtomic(inputPath, chunk); err != nil {
		return nil, err
	}

	var stdout string
	if backend == "shuffledns" {
		args := []string{
			"-list", inputPath,
			"-r", opts.ResolversPath,
			"-silent",
			"-massdns", statuses["massdns"].Path,
		}
		stdout, err = p.runToolCapture(ctx, "shuffledns", args, nil)
	} else {
		args := []string{
			"-l", inputPath,
			"-r", opts.ResolversPath,
			"-silent",
		}
		stdout, err = p.runToolCapture(ctx, "dnsx", args, nil)
	}
	if err != nil {
		return nil, err
	}

	lines := make([]string, 0)
	for _, line := range strings.Split(stdout, "\n") {
		host := normalizeResolvedLine(line)
		if host == "" {
			continue
		}
		if inScope(host, opts.Domain) {
			lines = append(lines, host)
		}
	}

	return util.UniqueSortedLower(lines), nil
}

func (p *Pipeline) runTool(ctx context.Context, toolName string, args []string, extraEnv map[string]string) error {
	path, err := p.lookupTool(toolName)
	if err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = applyEnv(extraEnv)
	var stderr bytes.Buffer
	cmd.Stdout = io.Discard
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s failed: %w: %s", toolName, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func (p *Pipeline) runToolCapture(ctx context.Context, toolName string, args []string, extraEnv map[string]string) (string, error) {
	path, err := p.lookupTool(toolName)
	if err != nil {
		return "", err
	}

	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = applyEnv(extraEnv)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s failed: %w: %s", toolName, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func (p *Pipeline) lookupTool(name string) (string, error) {
	for _, spec := range toolchain.KnownTools() {
		if spec.Name != name {
			continue
		}
		status := p.Tools.Find(spec)
		if !status.Available {
			return "", fmt.Errorf("tool %s is not available", name)
		}
		return status.Path, nil
	}
	return "", fmt.Errorf("unknown tool %s", name)
}

func applyEnv(extraEnv map[string]string) []string {
	env := append([]string{}, os.Environ()...)
	for key, value := range extraEnv {
		env = append(env, key+"="+value)
	}
	return env
}

func normalizeResolvedLine(line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}

	fields := strings.Fields(line)
	if len(fields) == 0 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(fields[0]))
}

func ExtractIntel(domain string, urls []string) ([]string, []string) {
	tokenSet := make(map[string]struct{})
	hostSet := make(map[string]struct{})

	for _, rawURL := range urls {
		rawURL = strings.TrimSpace(rawURL)
		if rawURL == "" {
			continue
		}

		parsed, err := url.Parse(rawURL)
		if err != nil || parsed.Host == "" {
			parsed, err = url.Parse("https://" + rawURL)
			if err != nil {
				continue
			}
		}

		host := strings.ToLower(parsed.Hostname())
		if inScope(host, domain) {
			hostSet[host] = struct{}{}
		}

		for _, segment := range strings.Split(parsed.Path, "/") {
			for _, token := range splitTokens(segment) {
				tokenSet[token] = struct{}{}
			}
		}
	}

	return sortedKeys(tokenSet), sortedKeys(hostSet)
}

func GeneratePermutations(domain string, passiveHosts, intelTokens, baseTokens []string, mode config.Mode) []string {
	stemSet := make(map[string]struct{})
	firstLabelSet := make(map[string]struct{})
	for _, host := range passiveHosts {
		host = strings.ToLower(strings.TrimSpace(host))
		if !inScope(host, domain) || host == domain {
			continue
		}
		prefix := strings.TrimSuffix(host, "."+domain)
		for _, label := range strings.Split(prefix, ".") {
			for _, token := range splitTokens(label) {
				stemSet[token] = struct{}{}
			}
		}

		parts := strings.Split(prefix, ".")
		if len(parts) > 0 {
			first := normalizeToken(parts[0])
			if first != "" {
				firstLabelSet[first] = struct{}{}
				stemSet[first] = struct{}{}
			}
		}
	}

	tokenLimit := 200
	maxDigits := 2
	includeTwoLabel := true
	switch mode {
	case config.ModeFast:
		tokenLimit = 80
		maxDigits = 1
		includeTwoLabel = false
	case config.ModeDeep:
		tokenLimit = 600
		maxDigits = 3
	}

	allTokens := util.UniqueSortedLower(append(append(intelTokens, baseTokens...), sortedKeys(stemSet)...))
	if len(allTokens) > tokenLimit {
		allTokens = allTokens[:tokenLimit]
	}

	bases := sortedKeys(firstLabelSet)
	if len(bases) == 0 {
		bases = allTokens
	}
	if len(bases) > tokenLimit {
		bases = bases[:tokenLimit]
	}

	resultSet := make(map[string]struct{}, len(allTokens)*10)
	var mu sync.Mutex
	jobs := make(chan string)
	workers := 4
	if mode == config.ModeDeep {
		workers = 8
	}

	add := func(candidate string) {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if !inScope(candidate, domain) || candidate == domain {
			return
		}
		labels := strings.Split(strings.TrimSuffix(candidate, "."+domain), ".")
		if len(labels) > 2 {
			return
		}
		mu.Lock()
		resultSet[candidate] = struct{}{}
		mu.Unlock()
	}

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for base := range jobs {
				add(base + "." + domain)
				for _, token := range allTokens {
					add(base + "-" + token + "." + domain)
					add(token + "-" + base + "." + domain)
					if includeTwoLabel {
						add(token + "." + base + "." + domain)
						add(base + "." + token + "." + domain)
					}
				}
				for digits := 1; digits <= maxDigits; digits++ {
					limit := 1
					for i := 0; i < digits; i++ {
						limit *= 10
					}
					for value := 0; value < limit; value++ {
						add(fmt.Sprintf("%s%0*d.%s", base, digits, value, domain))
					}
				}
			}
		}()
	}

	go func() {
		defer close(jobs)
		for _, base := range bases {
			jobs <- base
		}
	}()

	wg.Wait()

	for _, token := range allTokens {
		add(token + "." + domain)
	}

	return sortedKeys(resultSet)
}

func splitTokens(value string) []string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return nil
	}

	var tokens []string
	var current strings.Builder
	flush := func() {
		token := normalizeToken(current.String())
		if token != "" {
			tokens = append(tokens, token)
		}
		current.Reset()
	}

	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			current.WriteRune(r)
		case r >= '0' && r <= '9':
			current.WriteRune(r)
		default:
			flush()
		}
	}
	flush()
	return util.UniqueSortedLower(tokens)
}

func normalizeToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) < 2 || len(value) > 32 {
		return ""
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return ""
		}
	}
	return value
}

func inScope(host, domain string) bool {
	host = strings.Trim(strings.ToLower(host), ".")
	domain = strings.Trim(strings.ToLower(domain), ".")
	return host == domain || strings.HasSuffix(host, "."+domain)
}

func sortedKeys(items map[string]struct{}) []string {
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (p *Pipeline) logf(format string, args ...any) {
	if p.Stdout != nil {
		fmt.Fprintf(p.Stdout, format, args...)
	}
}
