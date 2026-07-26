# lazyssh

A terminal UI for managing SSH connections. Browse, add, edit, and connect to SSH hosts from your keyboard — no manual `ssh` commands needed.

Reads and writes `~/.ssh/config` directly. Persistent metadata (pin state, last seen, connection count) is stored in `~/.lazyssh/metadata.json`.

---

## Features

- **Server list** — scrollable list with live ping indicators, pin badges, and active session markers
- **Embedded terminal** — open SSH sessions in a split-panel PTY without leaving the TUI (`→`)
- **Full-screen sessions** — tab manager for multiple concurrent SSH sessions (`o`)
- **Port forwarding** — start/stop background `ssh -N` forwarding per host (`F` / `x`)
- **SFTP file browser** — dual-panel local↔remote file manager with upload, download, rename, delete (`f`)
- **Live search** — fuzzy filter by alias/host, `#tag` support (`/`)
- **Form editor** — 5-tab form covering ~30 SSH config fields, with config-block preview before saving
- **Auto-backup** — `~/.ssh/config` is backed up automatically before any write

---

## Installation

### Download binary

Grab the latest release from the [Releases](https://github.com/linpr/lazyssh/releases) page and place it on your `$PATH`:

```sh
# Linux AMD64
curl -L https://github.com/linpr/lazyssh/releases/latest/download/lazyssh-linux-amd64 -o lazyssh
chmod +x lazyssh
sudo mv lazyssh /usr/local/bin/
```

### Build from source

Requires Go 1.25+.

```sh
git clone https://github.com/linpr/lazyssh.git
cd lazyssh
go build -o lazyssh ./cmd/
```

With [Task](https://taskfile.dev):

```sh
task build
```

### go install

```sh
go install github.com/linpr/lazyssh/cmd@latest
```

---

## Usage

```sh
lazyssh              # launch TUI
lazyssh --version    # print version and commit hash
lazyssh -v           # same
```

---

## Keyboard Shortcuts

### Server List

| Key | Action |
|-----|--------|
| `j` / `k` or `↑` / `↓` | Navigate list |
| `Enter` | Connect (suspends TUI, restores on exit) |
| `→` | Open embedded PTY session in right panel |
| `n` | Add new server |
| `e` | Edit selected server |
| `d` | Delete selected server |
| `p` | Pin / unpin server |
| `g` | Ping server (TCP check on SSH port) |
| `F` | Start port forwarding (background) |
| `x` | Stop port forwarding |
| `f` | Open SFTP file browser |
| `/` | Search (fuzzy, `#tag` filter) |
| `s` | Cycle sort order |
| `?` | Toggle help overlay |
| `q` | Quit |

### Embedded Sessions

| Key | Action |
|-----|--------|
| `o` | Open session in full-screen tab view |
| `Ctrl+\` | Return to list (session stays alive) |

### Full-Screen Session View

| Key | Action |
|-----|--------|
| `Ctrl+N` | Next tab |
| `Ctrl+P` | Previous tab |
| `Ctrl+\` | Return to list |

### SFTP File Browser

| Key | Action |
|-----|--------|
| `Tab` | Switch panel (local / remote) |
| `Enter` | Enter directory |
| `Backspace` | Go up |
| `u` | Upload (local → remote) |
| `d` | Download (remote → local) |
| `n` | New directory |
| `r` | Rename |
| `x` | Delete |
| `q` | Close browser |

---

## Configuration

lazyssh reads `~/.ssh/config` using standard SSH config format. No additional config file is required.

Metadata is stored in `~/.lazyssh/metadata.json` and is managed automatically.

Logs are written to `~/.lazyssh/lazyssh.log`.

---

## Building from Source

```sh
# with Task
task build    # produces ./lazyssh
task fmt      # go fmt
task lint     # golangci-lint
task test     # go test ./...

# plain go
go build -ldflags "-X github.com/linpr/lazyssh/cmd.version=$(git describe --tags --always --dirty)" -o lazyssh ./cmd/
```

---

## Release

Releases are built automatically by GitHub Actions when a `v*` tag is pushed:

```sh
git tag v1.0.0
git push origin v1.0.0
```

The workflow builds binaries for Linux, macOS, and Windows (amd64 + arm64), generates `checksums.txt`, and publishes a GitHub Release with auto-generated release notes.

---

## License

Apache License 2.0. See [LICENSE](LICENSE).
