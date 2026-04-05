package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// AutoInstallChromeEnabled returns true when the environment requests
// automatic installation of Chrome/Chromium.
func AutoInstallChromeEnabled() bool {
	v := strings.TrimSpace(os.Getenv("RECON_AUTO_INSTALL_CHROME"))
	if v == "" {
		v = strings.TrimSpace(os.Getenv("AUTO_INSTALL_CHROME"))
	}
	if v == "" {
		return false
	}
	v = strings.ToLower(v)
	return v == "1" || v == "true" || v == "yes"
}

// InstallChrome attempts to install Chrome/Chromium using a best-effort set
// of package manager commands for the current OS. It returns nil on first
// successful install or an error describing the failures.
func InstallChrome(ctx context.Context) error {
	var cmds [][]string

	switch runtime.GOOS {
	case "linux":
		if _, err := exec.LookPath("apt-get"); err == nil {
			cmds = append(cmds, []string{"apt-get", "update"})
			cmds = append(cmds, []string{"apt-get", "install", "-y", "chromium-browser"})
			cmds = append(cmds, []string{"apt-get", "install", "-y", "google-chrome-stable"})
		}
		if _, err := exec.LookPath("yum"); err == nil {
			cmds = append(cmds, []string{"yum", "install", "-y", "chromium"})
			cmds = append(cmds, []string{"yum", "localinstall", "-y", "https://dl.google.com/linux/direct/google-chrome-stable_current_x86_64.rpm"})
		}
		if _, err := exec.LookPath("snap"); err == nil {
			cmds = append(cmds, []string{"snap", "install", "chromium"})
		}
	case "darwin":
		if _, err := exec.LookPath("brew"); err == nil {
			cmds = append(cmds, []string{"brew", "install", "--cask", "google-chrome"})
			cmds = append(cmds, []string{"brew", "install", "--cask", "chromium"})
		}
	case "windows":
		if _, err := exec.LookPath("choco"); err == nil {
			cmds = append(cmds, []string{"choco", "install", "googlechrome", "-y"})
		}
		if _, err := exec.LookPath("winget"); err == nil {
			cmds = append(cmds, []string{"winget", "install", "--id", "Google.Chrome", "-e"})
		}
	default:
		return errors.New("unsupported OS for auto-install")
	}

	if len(cmds) == 0 {
		return errors.New("no package manager found to perform auto-install")
	}

	var lastErr error
	for _, c := range cmds {
		cmd := exec.CommandContext(ctx, c[0], c[1:]...)
		out, err := cmd.CombinedOutput()
		if err == nil {
			return nil
		}
		lastErr = fmt.Errorf("command %v failed: %v - output: %s", strings.Join(c, " "), err, string(out))
	}

	if lastErr == nil {
		lastErr = errors.New("auto-install attempts failed")
	}
	return lastErr
}
