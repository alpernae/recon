# Recon Framework

A cross-platform recon CLI for Linux, macOS, Windows PowerShell, WSL2, and Docker Desktop.

It keeps the original workflow shape:

- passive enum
- URL intel extraction
- scoped permutations
- DNS resolution
- live probing
- nuclei scanning

The orchestration is now a single Go binary instead of Bash, so it works natively on Windows without requiring shell-script compatibility layers.

## Project Layout

```text
recon-framework/
├── cmd/
│   └── recon/
│       └── main.go
├── internal/
│   ├── app/
│   ├── config/
│   ├── install/
│   ├── pipeline/
│   ├── toolchain/
│   └── util/
├── wordlists/
│   └── base_tokens.txt
├── Dockerfile
├── go.mod
├── README.md
└── resolvers.txt
```

## Build

### Linux / macOS

```bash
go build -o recon ./cmd/recon
```

### Windows PowerShell

```powershell
go build -o recon.exe .\cmd\recon
```

## Commands

### Run recon

```bash
recon example.com
recon example.com --fast
recon example.com --deep --runtime native
recon example.com --runtime docker
recon example.com --outdir custom-output
```

### Doctor

```bash
recon doctor
recon doctor --runtime native
recon doctor --runtime docker
```

### Install tools locally

```bash
recon install-tools
recon install-tools --tools-dir .tools/custom
recon install-tools --force
```

Installed binaries are placed under:

```text
.tools/<os>-<arch>/bin/
```

The CLI prefers that local tool directory before falling back to the global `PATH`.

## Environment Variables

- `CHAOS_API_KEY`
- `RECON_RESOLVERS`
- `SUBFINDER_PROVIDER_CONFIG`
- `AMASS_CONFIG`
- `RECON_DOCKER_IMAGE`

Each one also has a matching CLI flag.

## Output Layout

```text
output-example.com/
├── passive/
│   ├── all.txt
│   ├── amass.txt
│   ├── chaos.txt
│   ├── final.txt
│   └── subfinder.txt
├── resolved/
│   ├── all.txt
│   ├── clean.txt
│   ├── passive.txt
│   └── perms.txt
├── perms/
│   └── all.txt
├── intel/
│   ├── tokens.txt
│   └── urls.txt
├── live/
│   └── live.txt
└── scan/
    └── nuclei.txt
```

## Windows Native

1. Build the CLI:

```powershell
go build -o recon.exe .\cmd\recon
```

2. Install the external tools into the local project directory (default) or globally:

```powershell
.\recon.exe install-tools                # installs into workspace .tools/<os>-<arch>/bin
.\recon.exe install-tools --tools-dir .tools/custom
.\recon.exe install-tools --global      # installs into GOBIN (or GOPATH/bin), falls back to PATH
```

3. Run a preflight check:

```powershell
.\recon.exe doctor --runtime native
```

4. Execute the workflow:

```powershell
.\recon.exe example.com --runtime native
```

Notes:

- Native Windows uses the internal Go stages plus external tools installed into `.tools\windows-amd64\bin`.
- If `shuffledns` and `massdns` are not both available, native Windows falls back to the built-in `dnsx` resolver path.
- `CHAOS_API_KEY` is optional. If it is missing, the Chaos stage is skipped with a warning.

## Windows WSL2

WSL2 is a first-class path for Linux-equivalent runs.

Inside WSL:

```bash
go build -o recon ./cmd/recon
./recon install-tools
./recon doctor --runtime native
./recon example.com --runtime native
```

Use this path when you want the Linux toolchain directly in a Windows-hosted environment.

## Windows Docker Desktop

Build the image:

```powershell
docker build -t recon-framework:latest .
```

Run the workflow inside Docker:

```powershell
docker run --rm -v "${PWD}:/app" -w /app recon-framework:latest example.com
```

Or let the host binary delegate automatically:

```powershell
.\recon.exe example.com --runtime docker
```

`--runtime auto` prefers Docker on Windows when the high-parity `shuffledns` + `massdns` backend is unavailable locally.

## Linux / macOS Quick Start

```bash
go build -o recon ./cmd/recon
./recon install-tools
./recon doctor
./recon example.com
```

## Troubleshooting

Use `recon doctor` first. The most common outcomes are:

- `missing required tool: <name>`: run `recon install-tools` or install that binary globally.
- `resolver list not found`: point `--resolvers` to a valid file or set `RECON_RESOLVERS`.
- `CHAOS_API_KEY is not set`: optional warning only; set the key to enable the Chaos stage.
- `docker runtime selected but image recon-framework:latest is not available`: build the image first with `docker build -t recon-framework:latest .`.
- `docker runtime only supports paths inside <cwd>`: keep `--outdir`, `--resolvers`, and config files inside the mounted workspace when using `--runtime docker`.

## Verification

This repo includes unit tests for:

- mode and output path handling
- tool discovery and runtime resolution
- installer asset selection and archive extraction
- URL intel extraction and permutation rules
- an end-to-end mocked pipeline run with rerun safety

Run them with:

```bash
go test ./...
```

## Disclaimer

Use this tooling only against systems you are authorized to assess.
