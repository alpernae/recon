Auto-install missing tools

The recon runner can attempt to auto-install missing required tools before a run. This is a convenience for CI or disposable environments but should be used with caution on personal machines.

Environment variables

- `RECON_AUTO_INSTALL_TOOLS=1` — enable auto-install of missing tools (required tools only by default).
- `RECON_AUTO_INSTALL_TOOLS_INCLUDE_OPTIONAL=1` — include optional tools in the auto-install process.

Behavior

- When enabled, the runner will call the built-in installer (same logic as `recon install-tools`) and attempt to fetch and install known tooling into the local tools directory (workspace `.tools/<os>-<arch>/bin` by default).
- Installers use `go install` when a `GoModule` is available for a tool; otherwise they attempt to download a GitHub release asset and extract the binary.
- The installer may require network access and appropriate privileges (for example to write to the target bin directory). If `--global` or global installation is requested, administrative privileges may be necessary.

Caveats

- Auto-install runs package downloads and binary writes. Only enable it in trusted environments (CI runners, disposable containers, or machines you control).
- Some optional backends (like `massdns`) may not have an automatic installer and will be skipped.
- If installation fails, the recon run will abort with an error explaining which tools could not be installed.

Examples

Enable auto-install for required tools only:

```bash
export RECON_AUTO_INSTALL_TOOLS=1
./recon example.com
```

Enable auto-install including optional tools:

```bash
export RECON_AUTO_INSTALL_TOOLS=1
export RECON_AUTO_INSTALL_TOOLS_INCLUDE_OPTIONAL=1
./recon example.com
```
