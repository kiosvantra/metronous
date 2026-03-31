<p align="center">
  <img src="assets/logo.png" alt="Metronous" width="100%"/>
</p>

# Metronous

> Local AI agent telemetry, benchmarking, and model calibration for OpenCode agents.

*Originally developed within the Gentle AI ecosystem.*

Metronous tracks every tool call, session, and cost from your OpenCode agents — then runs weekly benchmarks to tell you which agents are underperforming and which model would save you money.

## What it does

- **Tracks** agent sessions, tool calls, tokens, and cost in real-time
- **Benchmarks** each agent with a defined mission against its performance criteria
- **Recommends** model switches with estimated cost savings
- **Visualizes** everything in a terminal dashboard (TUI)

## Architecture

> For component details and protocols, see [docs/architecture.md](docs/architecture.md).  
> For benchmark methodology, see [docs/how-it-works.md](docs/how-it-works.md).

```
OpenCode → metronous mcp (shim) → HTTP → metronous daemon (system service) → SQLite
                                                                        ↓
                                                              ./metronous dashboard
```

- **Shim (metronous mcp)**: stdio↔HTTP bridge launched by OpenCode plugin, forwards MCP calls to the daemon
- **Daemon (metronous)**: Long-lived system service (systemd on Linux, Windows SCM on Windows) that handles telemetry ingestion, storage, and weekly benchmarks
- **HTTP Endpoint**: Dynamic port (written to `~/.metronous/data/mcp.port`) for shim-to-daemon communication
- **TUI Dashboard**: 3-tab terminal UI (Tracking / Benchmark / Config)

## Prerequisites

- [OpenCode](https://opencode.ai) installed and configured
- OpenCode agents configured (e.g., from Gentle AI's SDD suite)

Go 1.22+ is only required for source builds and `go install`.

## Installation

### Support matrix

- **Linux**: official install flow
- **Windows**: experimental/manual
- **macOS**: manual CLI only

### Linux release install (recommended)

```bash
VERSION=$(curl -sSL https://api.github.com/repos/kiosvantra/metronous/releases/latest | grep '"tag_name"' | grep -oE 'v[0-9]+\.[0-9]+\.[0-9]+' | head -1)
ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
TARBALL="metronous_${VERSION#v}_linux_${ARCH}.tar.gz"
curl -fsSLO "https://github.com/kiosvantra/metronous/releases/download/${VERSION}/${TARBALL}"
curl -fsSLO "https://github.com/kiosvantra/metronous/releases/download/${VERSION}/checksums.txt"
sha256sum -c --ignore-missing checksums.txt
tar -xzf "${TARBALL}"
mkdir -p ~/.local/bin
install -m 0755 ./metronous ~/.local/bin/metronous
rm -f "${TARBALL}" checksums.txt metronous-plugin.ts
```

Then run the installed binary:

```bash
~/.local/bin/metronous install
```

Do not run `metronous install` with `sudo` or as root. It must run as the same normal user that runs OpenCode.

### Via Go (Linux, with systemd user services)

```bash
go install github.com/kiosvantra/metronous/cmd/metronous@latest
# Ensure the installed binary is on your PATH, then run:
metronous install
```

If you use `GOBIN`, run the binary from that directory instead of `GOPATH/bin`.

### Manual/source build

```bash
git clone https://github.com/kiosvantra/metronous
cd metronous
go build -o metronous ./cmd/metronous
```

Linux:

```bash
mkdir -p ~/.local/bin
install -m 0755 ./metronous ~/.local/bin/metronous
~/.local/bin/metronous install
```

### Windows manual testing flow (experimental)

```powershell
# Download the matching Windows archive from GitHub Releases,
# for example: metronous_<version>_windows_amd64.zip
# Run PowerShell as Administrator before continuing.
$archive = "metronous_<version>_windows_amd64.zip"
Expand-Archive -Path $archive -DestinationPath .\metronous-release -Force
$exe = Get-ChildItem .\metronous-release -Recurse -Filter metronous.exe | Select-Object -First 1
$dest = "$env:LOCALAPPDATA\Programs\Metronous"
New-Item -ItemType Directory -Force -Path $dest | Out-Null
Move-Item $exe.FullName "$dest\metronous.exe" -Force
& "$dest\metronous.exe" install
```

Optionally verify the archive before extracting it by comparing its SHA-256 hash with `checksums.txt` from the same release.

Run the elevated PowerShell session as the same Windows user account that runs OpenCode.

Windows support is currently experimental. The native service/install flow is still being hardened, so Linux is the only officially supported installer path.

### macOS status

```bash
go build -o metronous ./cmd/metronous
./metronous init
./metronous server --data-dir ~/.metronous/data --daemon-mode
```

macOS currently supports the CLI only. `metronous install`, automatic OpenCode patching, and automatic plugin installation are not supported on macOS.

### Windows service notes

```powershell
& "$env:LOCALAPPDATA\Programs\Metronous\metronous.exe" install
```

> **Note:** `metronous install` on Windows requires an elevated terminal (Run as Administrator) to register the Windows service. Use `& "$env:LOCALAPPDATA\Programs\Metronous\metronous.exe" service status` or `sc query metronous` to verify.

For manual control:
```powershell
& "$env:LOCALAPPDATA\Programs\Metronous\metronous.exe" service start     # Start the service
& "$env:LOCALAPPDATA\Programs\Metronous\metronous.exe" service stop      # Stop the service
& "$env:LOCALAPPDATA\Programs\Metronous\metronous.exe" service status    # Check service status
& "$env:LOCALAPPDATA\Programs\Metronous\metronous.exe" service uninstall # Remove the service
```

### Configure OpenCode (automatically done by `metronous install` on Linux)

After running `metronous install` on Linux, your OpenCode will be configured with:

1. **MCP shim**: the installed executable path plus `mcp` for telemetry ingestion
2. **OpenCode plugin**: `metronous.ts` copied to `~/.config/opencode/plugins/`

The plugin captures agent sessions and forwards events to the daemon via HTTP.

Then restart OpenCode and it will show **"Metronous Connected"**.

## Usage

### Dashboard

```bash
metronous dashboard
```

- **[1] Tracking** — Real-time event stream with tokens and cost per tool call
- **[2] Benchmark** — Agent performance history with verdict, recommended model, and savings estimate
- **[3] Config** — Edit performance thresholds (saved to `~/.metronous/thresholds.json`)

### Manual benchmark

```bash
METRONOUS_DATA_DIR=~/.metronous/data go run cmd/run-benchmark/main.go
```

## Data directory

All data lives in `~/.metronous/`:

```
~/.metronous/
├── data/
│   ├── tracking.db      # Event telemetry (SQLite)
│   ├── benchmark.db     # Benchmark run history (SQLite)
│   ├── mcp.port         # Dynamic HTTP port (runtime)
│   └── metronous.pid    # Server PID (runtime)
└── thresholds.json      # Performance thresholds (editable via TUI)
```

## Agents tracked

Metronous automatically discovers all agents from your OpenCode configuration:

- **Built-in agents**: `build`, `plan`, `general`, `explore`  
- **Custom agents**: any agent defined in `opencode.json` or `~/.config/opencode/agents/*.md` (at global or project level), with type `primary`, `subagent`, or `all`

For benchmarking, Metronous requires each agent to have a **mission** defined (via the `description` field in `opencode.json` or YAML frontmatter in the agent's markdown file). Here's an example with the Gentle AI SDD agents:

| Agent | Mission |
|-------|---------|
| `sdd-orchestrator` | Coordinates sub-agents, never does work inline |
| `sdd-apply` | Implements code changes from task definitions |
| `sdd-explore` | Investigates codebase and thinks through ideas |
| `sdd-verify` | Validates implementation against specs |
| `sdd-spec` | Writes detailed specifications from proposals |
| `sdd-design` | Creates technical design from proposals |
| `sdd-propose` | Creates change proposals from explorations |
| `sdd-tasks` | Breaks down specs and designs into tasks |
| `sdd-init` | Bootstraps SDD context and project configuration |
| `sdd-archive` | Archives completed change artifacts |

## License

MIT — see [LICENSE](LICENSE)
