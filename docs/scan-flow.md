# Scan Flow — recon-framework

This document explains end-to-end how a `recon` run works and exactly how `gau` output is consumed by the pipeline. It contains a detailed mermaid flow diagram and notes tying each step to the implementation.

## High-level flow

```mermaid
flowchart TD
  CLI["CLI: recon <domain> [flags]"] --> TOOLCHAIN["Toolchain: KnownTools, Discover, Find"]
  TOOLCHAIN --> RUNTIME["ResolveRuntime: native | docker"]
  RUNTIME -->|native| PIPE["Pipeline.Run()"]
  RUNTIME -->|docker| DOCKER["DelegateToDocker (docker run ...)"]

  subgraph Installer ["(Optional) Install tools if missing)"]
    INSTALLER["Installer: go install / GitHub release"]
  end
  TOOLCHAIN --> INSTALLER

  subgraph Pipeline ["Pipeline stages"]
    PIPE --> ENUM["runEnum() — passive enumeration"]
    ENUM --> AMASS["amass"]
    ENUM --> SUBFINDER["subfinder"]
    ENUM --> CHAOS["chaos"]
    AMASS --> PASSIVE_ALL["passive/all.txt"]
    SUBFINDER --> PASSIVE_ALL
    CHAOS --> PASSIVE_ALL

    PIPE --> INTEL["runIntel() — gau"]
    INTEL --> GAU["gau <domain> (external binary)"]
    GAU -->|stdout: URLs (one per line)| URLS["intel/urls.txt (raw)"]
    URLS --> EXTRACT["ExtractIntel(domain, urls)"]
    EXTRACT --> TOKENS["intel/tokens.txt (path tokens)"]
    EXTRACT --> ARCHIVED["archived hosts (subdomains)"]
    ARCHIVED --> PASSIVE_FINAL["passive/final.txt (passiveAll + archived)"]

    PIPE --> PERM["runPermute() — permutations using tokens + base_tokens"]
    PERM --> PERMS["perms/all.txt"]

    PIPE --> RESOLVE["runResolve() — choose backend"]
    RESOLVE -->|high-parity| SHUFFLEDNS["shuffledns + massdns"]
    RESOLVE -->|fallback| DNSX["dnsx"]
    SHUFFLEDNS --> RESOLVED["resolved/*.txt"]
    DNSX --> RESOLVED

    PIPE --> PROBE["runProbe() — httpx"]
    PROBE --> LIVE["live/live.txt"]

    PIPE --> SCAN["runScan() — nuclei"]
    SCAN --> NUCLEI["scan/nuclei.txt"]
  end

  PASSIVE_ALL --> PIPE

  style CLI fill:#fdf6e3,stroke:#333,stroke-width:1px
  style PIPE fill:#eef6ff,stroke:#333,stroke-width:1px
  style EXTRACT fill:#e8ffe8,stroke:#333,stroke-width:1px
```

## How the `gau` output is used (precise behavior)

- The pipeline runs `gau` in `runIntel()` with exactly the domain as the single argument:

  - Implementation: `p.runToolCapture(ctx, "gau", []string{opts.Domain}, nil)` ([internal/pipeline/pipeline.go](internal/pipeline/pipeline.go)).

- The app captures `gau`'s stdout (a single string), trims, splits on newlines and deduplicates the lines. The raw lines are written to `intel/urls.txt` and are preserved as-is (so full URL path and query are kept in that file).

- After saving the raw URLs, the pipeline calls `ExtractIntel(domain, urlLines)` to extract two things:
  1. `archivedHosts` — hostnames (subdomains) that are in-scope for the scanned domain.
  2. `tokens` — short path tokens extracted from URL path segments.

### ExtractIntel behavior (exact parsing rules)

- For each URL line:
  - Trim whitespace; skip if empty.
  - Attempt `url.Parse(rawURL)`. If parsing fails or `parsed.Host == ""`, it retries with `url.Parse("https://" + rawURL)` (this accepts lines missing scheme).
  - `host := strings.ToLower(parsed.Hostname())` — if `inScope(host, domain)` then the host is added to the archived-hosts set.
  - For the path: `for _, segment := range strings.Split(parsed.Path, "/") { for _, token := range splitTokens(segment) { tokenSet[token] = struct{}{} } }`

- `splitTokens()` rules:
  - Lowercases and splits on any non-alphanumeric rune.
  - `normalizeToken()` enforces token length between 2 and 32 and allows only letters and digits.
  - Result: only alpha-numeric tokens of length 2..32 are kept.

### Important practical details

- Subdomains: yes — any host present in `gau`'s output that is in-scope (ends with the domain) becomes an `archivedHost` and is appended to the passive host list:
  - `finalPassive := util.UniqueSortedLower(append(passiveAll, archivedHosts...))` and saved to `passive/final.txt` ([internal/pipeline/pipeline.go](internal/pipeline/pipeline.go)).

- Paths and parameters:
  - The full URL (including path and query string) is preserved in `intel/urls.txt`.
  - Token extraction only uses `parsed.Path` (the path component) — query parameters (the `?a=b` part) are _not_ parsed or tokenized.
  - Path segments are split on `/`, then non-alphanumeric characters inside segments are used as delimiters for tokens. Example: `/auth/login?v=1` → path segments `auth`, `login`; tokens `auth`, `login` (query `v=1` ignored for tokenization).

- Tokens are written to `intel/tokens.txt` and are used by `GeneratePermutations()` to create candidate subdomain permutations (combined with `wordlists/base_tokens.txt`). These permutations feed the resolver stage and can lead to discovering additional hosts.

- Robustness: if `gau` emits bare hostnames or non-scheme URLs, `ExtractIntel` attempts to parse them by prefixing `https://` before parsing.

## Where to look in the code

- **Run gau and capture output:** [internal/pipeline/pipeline.go](internal/pipeline/pipeline.go)
- **Extract parsing/token rules:** `ExtractIntel` and `splitTokens` — [internal/pipeline/pipeline.go](internal/pipeline/pipeline.go)
- **Tool discovery / KnownTools list:** [internal/toolchain/toolchain.go](internal/toolchain/toolchain.go)
- **Installer behavior (go install / github releases):** [internal/install/install.go](internal/install/install.go)
- **Integration test that mocks `gau` output:** [internal/pipeline/pipeline_integration_test.go](internal/pipeline/pipeline_integration_test.go)

## Quick implications for scanning strategy

- If you want path/query tokens to be used for permutation, ensure `gau` returns path segments (it typically will when aggregating Wayback / archive URLs).
- `gau`'s historical URLs are a good source of both archived subdomains and path tokens; the pipeline leverages both to broaden permutation and discovery.
- If you need query-string tokens included, we can extend `ExtractIntel` to also split `parsed.RawQuery` and extract tokens similarly.

---

If you'd like, I can (pick one):

- update `ExtractIntel` to also extract tokens from the query string, or
- add an example showing how `intel/urls.txt` looks for a sample domain and the tokens produced.

Created file: [docs/scan-flow.md](docs/scan-flow.md)
