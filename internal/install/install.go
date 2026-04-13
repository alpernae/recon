package install

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"unicode"

	"github.com/alpernae/recon/internal/config"
	"github.com/alpernae/recon/internal/toolchain"
	"github.com/alpernae/recon/internal/util"
)

type Installer struct {
	Toolchain  *toolchain.Toolchain
	Stdout     io.Writer
	Stderr     io.Writer
	HTTPClient *http.Client
}

type releaseResponse struct {
	Assets []releaseAsset `json:"assets"`
}

type releaseAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

func New(tc *toolchain.Toolchain, stdout, stderr io.Writer) *Installer {
	return &Installer{
		Toolchain: tc,
		Stdout:    stdout,
		Stderr:    stderr,
		HTTPClient: &http.Client{
			Timeout: 0,
		},
	}
}

func (i *Installer) Install(ctx context.Context, opts config.InstallOptions) error {
	binDir := i.Toolchain.BinDir
	if binDir == "" {
		binDir = config.ToolBinDir(opts.ToolRoot)
	}
	if err := util.EnsureDir(binDir); err != nil {
		return err
	}

	goAvailable := i.commandExists("go")
	for _, spec := range toolchain.KnownTools() {
		if spec.Optional && !opts.IncludeOptional {
			continue
		}
		// massdns: attempt to build from source if requested (no release binaries)

		localPath := filepath.Join(binDir, binaryName(spec.Binary, opts.TargetOS))
		if !opts.Force {
			if _, err := os.Stat(localPath); err == nil {
				i.logf("[=] %s already installed at %s\n", spec.Name, localPath)
				continue
			}
		}

		i.logf("[+] Installing %s...\n", spec.Name)
		var err error
		switch {
		case spec.Name == "massdns":
			err = i.installMassDNS(ctx, binDir, opts.TargetOS, opts.TargetArch)
		case goAvailable && spec.GoModule != "":
			err = i.installWithGo(ctx, spec, binDir, opts.TargetOS, opts.TargetArch)
		case spec.GitHubRepo != "":
			err = i.installFromGitHubRelease(ctx, spec, binDir, opts.TargetOS, opts.TargetArch)
		default:
			err = fmt.Errorf("no installer available")
		}

		if err != nil {
			if spec.Required {
				return fmt.Errorf("install %s: %w", spec.Name, err)
			}
			i.logf("[-] Optional tool %s was not installed: %v\n", spec.Name, err)
		}
	}

	return nil
}

func (i *Installer) Uninstall(ctx context.Context, opts config.InstallOptions) error {
	binDir := i.Toolchain.BinDir
	if binDir == "" {
		binDir = config.ToolBinDir(opts.ToolRoot)
	}

	for _, spec := range toolchain.KnownTools() {
		if spec.Optional && !opts.IncludeOptional {
			continue
		}
		// allow uninstall of massdns if present (it may have been built/installed by the installer)

		localPath := filepath.Join(binDir, binaryName(spec.Binary, opts.TargetOS))
		if err := os.Remove(localPath); err != nil {
			if os.IsNotExist(err) {
				i.logf("[=] %s not found at %s\n", spec.Name, localPath)
				continue
			}
			if spec.Required {
				return fmt.Errorf("remove %s: %w", spec.Name, err)
			}
			i.logf("[-] Optional tool %s was not removed: %v\n", spec.Name, err)
			continue
		}
		i.logf("[+] Removed %s from %s\n", spec.Name, localPath)
	}

	return nil
}

func (i *Installer) installWithGo(ctx context.Context, spec toolchain.ToolSpec, binDir, targetOS, targetArch string) error {
	cmd := exec.CommandContext(ctx, "go", "install", spec.GoModule)
	cmd.Env = append(os.Environ(),
		"GOBIN="+binDir,
		"GO111MODULE=on",
		"GOOS="+targetOS,
		"GOARCH="+targetArch,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (i *Installer) installFromGitHubRelease(ctx context.Context, spec toolchain.ToolSpec, binDir, targetOS, targetArch string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", spec.GitHubRepo), nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "github.com/alpernae/recon")

	response, err := i.HTTPClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("github api returned %s", response.Status)
	}

	var release releaseResponse
	if err := json.NewDecoder(response.Body).Decode(&release); err != nil {
		return err
	}

	asset, err := selectAsset(spec, release.Assets, targetOS, targetArch)
	if err != nil {
		return err
	}

	tmpFile, err := os.CreateTemp("", spec.Name+"-*")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	if err := i.download(ctx, asset.URL, tmpPath); err != nil {
		return err
	}

	return extractAsset(tmpPath, asset.Name, filepath.Join(binDir, binaryName(spec.Binary, targetOS)))
}

func (i *Installer) download(ctx context.Context, assetURL, dst string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "github.com/alpernae/recon")

	response, err := i.HTTPClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("download returned %s", response.Status)
	}

	file, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = io.Copy(file, response.Body)
	return err
}

func selectAsset(spec toolchain.ToolSpec, assets []releaseAsset, targetOS, targetArch string) (releaseAsset, error) {
	type candidate struct {
		asset releaseAsset
		score int
	}

	var matches []candidate
	for _, asset := range assets {
		score := assetScore(spec, asset.Name, targetOS, targetArch)
		if score > 0 {
			matches = append(matches, candidate{asset: asset, score: score})
		}
	}
	if len(matches) == 0 {
		return releaseAsset{}, fmt.Errorf("no matching release asset found for %s on %s/%s", spec.Name, targetOS, targetArch)
	}

	slices.SortFunc(matches, func(a, b candidate) int {
		switch {
		case a.score > b.score:
			return -1
		case a.score < b.score:
			return 1
		default:
			return strings.Compare(a.asset.Name, b.asset.Name)
		}
	})

	return matches[0].asset, nil
}

func assetScore(spec toolchain.ToolSpec, name, targetOS, targetArch string) int {
	lower := strings.ToLower(name)
	if !strings.Contains(lower, strings.ToLower(spec.Binary)) && !strings.Contains(lower, strings.ToLower(spec.Name)) {
		return 0
	}

	score := 1
	for _, token := range osTokens(targetOS) {
		if containsDelimitedToken(lower, token) {
			score += 4
			break
		}
	}
	for _, token := range archTokens(targetArch) {
		if containsDelimitedToken(lower, token) {
			score += 4
			break
		}
	}
	switch {
	case strings.HasSuffix(lower, ".zip"):
		score += 3
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		score += 3
	case strings.HasSuffix(lower, ".exe"), !strings.Contains(lower, "."):
		score += 1
	}
	return score
}

func osTokens(targetOS string) []string {
	switch targetOS {
	case "windows":
		return []string{"windows", "win64", "win"}
	case "darwin":
		return []string{"darwin", "macos", "mac"}
	default:
		return []string{targetOS}
	}
}

func archTokens(targetArch string) []string {
	switch targetArch {
	case "amd64":
		return []string{"amd64", "x86_64", "64bit"}
	case "arm64":
		return []string{"arm64", "aarch64"}
	default:
		return []string{targetArch}
	}
}

func containsDelimitedToken(value, token string) bool {
	valueTokens := splitAssetTokens(value)
	tokenTokens := splitAssetTokens(token)
	if len(tokenTokens) == 0 {
		return false
	}

	for index := 0; index <= len(valueTokens)-len(tokenTokens); index++ {
		matched := true
		for offset := range tokenTokens {
			if valueTokens[index+offset] != tokenTokens[offset] {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}

	return false
}

func splitAssetTokens(value string) []string {
	return strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

func extractAsset(assetPath, assetName, destination string) error {
	switch {
	case strings.HasSuffix(strings.ToLower(assetName), ".zip"):
		return extractZip(assetPath, destination)
	case strings.HasSuffix(strings.ToLower(assetName), ".tar.gz"), strings.HasSuffix(strings.ToLower(assetName), ".tgz"):
		return extractTarGz(assetPath, destination)
	default:
		return copyBinary(assetPath, destination)
	}
}

func extractZip(assetPath, destination string) error {
	archive, err := zip.OpenReader(assetPath)
	if err != nil {
		return err
	}
	defer archive.Close()

	for _, file := range archive.File {
		if !archiveEntryMatchesBinary(file.Name, destination) {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			return err
		}
		defer reader.Close()
		return writeExtractedBinary(destination, reader)
	}
	return fmt.Errorf("binary not found in zip archive")
}

func extractTarGz(assetPath, destination string) error {
	file, err := os.Open(assetPath)
	if err != nil {
		return err
	}
	defer file.Close()

	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if !archiveEntryMatchesBinary(header.Name, destination) {
			continue
		}
		return writeExtractedBinary(destination, tarReader)
	}

	return fmt.Errorf("binary not found in tar archive")
}

func archiveEntryMatchesBinary(entryName, destination string) bool {
	entryBase := strings.ToLower(filepath.Base(entryName))
	targetBase := strings.ToLower(filepath.Base(destination))
	return entryBase == targetBase || strings.TrimSuffix(entryBase, filepath.Ext(entryBase)) == strings.TrimSuffix(targetBase, filepath.Ext(targetBase))
}

func copyBinary(src, dst string) error {
	if err := util.CopyFile(src, dst); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		return os.Chmod(dst, 0o755)
	}
	return nil
}

func writeExtractedBinary(destination string, reader io.Reader) error {
	if err := util.EnsureDir(filepath.Dir(destination)); err != nil {
		return err
	}
	file, err := os.Create(destination)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := io.Copy(file, reader); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		return os.Chmod(destination, 0o755)
	}
	return nil
}

func (i *Installer) installMassDNS(ctx context.Context, binDir, targetOS, targetArch string) error {
	if !i.commandExists("git") {
		return fmt.Errorf("git not found: required to build massdns from source")
	}
	if !i.commandExists("make") {
		return fmt.Errorf("make not found: required to build massdns from source")
	}

	tmpDir, err := os.MkdirTemp("", "massdns-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", "https://github.com/blechschmidt/massdns.git", tmpDir)
	cmd.Env = os.Environ()
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone failed: %w: %s", err, strings.TrimSpace(string(out)))
	}

	cmd = exec.CommandContext(ctx, "make", "-C", tmpDir)
	cmd.Env = os.Environ()
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("make failed: %w: %s", err, strings.TrimSpace(string(out)))
	}

	src := filepath.Join(tmpDir, "bin", "massdns")
	dst := filepath.Join(binDir, binaryName("massdns", targetOS))
	if err := util.CopyFile(src, dst); err != nil {
		return fmt.Errorf("copy massdns binary: %w", err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(dst, 0o755); err != nil {
			return err
		}
	}
	i.logf("[+] massdns built and installed to %s\n", dst)
	return nil
}

func binaryName(name, targetOS string) string {
	if targetOS == "windows" && !strings.HasSuffix(name, ".exe") {
		return name + ".exe"
	}
	return name
}

func (i *Installer) commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func (i *Installer) logf(format string, args ...any) {
	if i.Stdout != nil {
		fmt.Fprintf(i.Stdout, format, args...)
	}
}
