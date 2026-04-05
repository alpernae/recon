package pipeline

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/alpernae/recon/internal/install"
	"github.com/alpernae/recon/internal/util"

	"github.com/chromedp/chromedp"
)

// CaptureScreenshot navigates to targetURL in a headless browser and writes a full-page
// PNG screenshot to outPath. Returns an error if the browser or capture fails.
func CaptureScreenshot(ctx context.Context, targetURL, outPath string) error {
	// Ensure output directory exists
	if err := util.EnsureDir(filepath.Dir(outPath)); err != nil {
		return err
	}

	// Create an allocator with headless flags
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-setuid-sandbox", true),
	)

	// Find Chrome/Chromium executable (supports CHROME_PATH env var).
	// If not found and auto-install is enabled, attempt to install then re-check.
	execPath, err := findChromeExec()
	if err != nil || execPath == "" {
		if install.AutoInstallChromeEnabled() {
			if ierr := install.InstallChrome(ctx); ierr != nil {
				return fmt.Errorf("chrome not found and auto-install failed: %v (lookup error: %v)", ierr, err)
			}
			execPath, err = findChromeExec()
			if err != nil || execPath == "" {
				return fmt.Errorf("chrome not found after auto-install: %v", err)
			}
		} else {
			return err
		}
	}
	opts = append(opts, chromedp.ExecPath(execPath))

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, opts...)
	defer cancelAlloc()

	ctx, cancelCtx := chromedp.NewContext(allocCtx)
	defer cancelCtx()

	// Timeout for navigation and screenshot
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var buf []byte
	tasks := chromedp.Tasks{
		chromedp.Navigate(targetURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Sleep(2 * time.Second),
		chromedp.FullScreenshot(&buf, 90),
	}

	if err := chromedp.Run(ctx, tasks); err != nil {
		return err
	}

	return os.WriteFile(outPath, buf, 0o644)
}

// findChromeExec attempts to locate a Chrome/Chromium executable on the host.
// It checks common environment variables, the PATH, and well-known install locations
// across Linux, macOS, and Windows.
func findChromeExec() (string, error) {
	// Allow explicit override via env vars
	envVars := []string{"CHROME_PATH", "CHROME_BIN", "GOOGLE_CHROME_SHIM", "GOOGLE_CHROME_BIN"}
	for _, v := range envVars {
		if p := strings.TrimSpace(os.Getenv(v)); p != "" {
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
			if p2, err := exec.LookPath(p); err == nil {
				return p2, nil
			}
		}
	}

	// Try common binary names on PATH
	names := []string{"google-chrome-stable", "google-chrome", "chrome", "chromium-browser", "chromium"}
	for _, n := range names {
		if p, err := exec.LookPath(n); err == nil {
			return p, nil
		}
	}

	// OS-specific well-known locations
	switch runtime.GOOS {
	case "windows":
		winPaths := []string{
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
		}
		for _, p := range winPaths {
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
		}
	case "darwin":
		macPaths := []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		}
		for _, p := range macPaths {
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
		}
	case "linux":
		linuxPaths := []string{
			"/usr/bin/google-chrome-stable",
			"/usr/bin/google-chrome",
			"/usr/bin/chromium-browser",
			"/usr/bin/chromium",
			"/snap/bin/chromium",
		}
		for _, p := range linuxPaths {
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
		}
	}

	return "", errors.New("no Chrome/Chromium executable found; install Chrome/Chromium or set CHROME_PATH env var")
}
