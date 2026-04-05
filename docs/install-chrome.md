Installing Chrome/Chromium for headless screenshots

This project uses Chrome/Chromium (via chromedp) to capture screenshots. Ensure a Chrome/Chromium binary is installed and either available on your PATH or set via the `CHROME_PATH` environment variable.

Linux (Debian/Ubuntu)

- Install Google Chrome (recommended):

```sh
sudo apt update
sudo apt install -y wget gnupg ca-certificates
wget -q -O - https://dl.google.com/linux/linux_signing_key.pub | sudo apt-key add -
sudo sh -c 'echo "deb [arch=amd64] http://dl.google.com/linux/chrome/deb/ stable main" > /etc/apt/sources.list.d/google-chrome.list'
sudo apt update
sudo apt install -y google-chrome-stable
```

- Or install Chromium:

```sh
sudo apt update
sudo apt install -y chromium-browser
```

Other Linux (RHEL/CentOS)

```sh
sudo yum localinstall -y https://dl.google.com/linux/direct/google-chrome-stable_current_x86_64.rpm
```

Snap-based systems:

```sh
sudo snap install chromium
```

macOS

- Using Homebrew:

```sh
brew install --cask google-chrome
# or
brew install --cask chromium
```

Windows

- Using Chocolatey:

```powershell
choco install googlechrome -y
```

- Or download and run the official installer from https://www.google.com/chrome/

Set `CHROME_PATH` (optional)

If the binary is installed in a non-standard location, set the `CHROME_PATH` environment variable to point to the executable.

- Linux / macOS:

```sh
export CHROME_PATH=/usr/bin/google-chrome
```

- Windows (PowerShell):

```powershell
setx CHROME_PATH "C:\Program Files\Google\Chrome\Application\chrome.exe"
```

Notes

- The code will attempt to auto-detect common Chrome/Chromium locations. If it still fails, set `CHROME_PATH` to the full path of the browser.
- For CI environments, consider installing `google-chrome-stable` or `chromium` via your CI's package manager and exporting `CHROME_PATH` if necessary.

Auto-install (Optional)

The runtime can attempt to auto-install Chrome/Chromium when a binary is not found. This is *disabled by default* and must be enabled by setting one of the following environment variables:

- `RECON_AUTO_INSTALL_CHROME=1`
- `AUTO_INSTALL_CHROME=true`

When enabled, the program will try a small set of package-manager commands for the current OS (for example `apt-get`, `yum`, `snap`, `brew`, `choco`, or `winget`) and will stop once an install command succeeds. Note:

- Auto-install commands may require root/administrator privileges (for example `sudo` on Linux). Use this only in CI or on systems where you understand the effect of running package managers.
- If no suitable package manager is found or all install attempts fail, the program will return an error. In that case, install Chrome/Chromium manually or set `CHROME_PATH`.

Enable auto-install only when you trust the environment (CI runners, disposable containers, or your own machine).

CLI flag

You can also request installation from the CLI when starting a recon run by passing `--install-chrome`:

```bash
recon example.com --install-chrome
```

This will attempt the same platform-specific installation commands described above. Use this only on machines you control because package managers may require elevated privileges.
