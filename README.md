<h1 align="center">Visual Studio Code Portable</h1>

<p align="center">
  <img src="code.png" alt="Visual Studio Code portable launcher" width="120">
</p>

<p align="center">
  <strong>Portable Visual Studio Code for Windows.</strong><br>
  A dependency-free, single-EXE launcher that downloads, runs, and auto-updates
  VS Code entirely from its own folder — no installer, no DLLs, no traces.
</p>

<p align="center">
  <a href="https://github.com/jboxberger/codeportable/releases/latest/download/CodePortable.exe"><img src="https://img.shields.io/badge/download-CodePortable.exe-2ea44f?logo=windows" alt="Download CodePortable.exe"></a>
  <a href="https://github.com/jboxberger/codeportable/releases/latest"><img src="https://img.shields.io/github/v/release/jboxberger/codeportable?label=release" alt="Latest release"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/jboxberger/codeportable" alt="License: MIT"></a>
  <img src="https://img.shields.io/badge/platform-Windows%20x64-blue" alt="Platform: Windows x64">
  <img src="https://img.shields.io/badge/built%20with-Go-00ADD8?logo=go" alt="Built with Go">
</p>

---

## What it is

**CodePortable** turns [Visual Studio Code](https://code.visualstudio.com/)
into a truly portable Windows application. Drop a single `CodePortable.exe`
onto a USB stick, a network share, or any folder — on first run it fetches the
latest official VS Code build, unpacks it next to itself, and keeps every
setting, extension and bit of history inside that same folder. Move the folder,
and your whole editor moves with it.

It is a small, self-contained launcher written in Go: **one executable, no
bundled DLLs, no .NET/runtime dependency, no console window.** All UI —
download progress, update prompt, error dialogs — is drawn with the native
Windows API.

## Why

- **Truly portable.** Visual Studio Code plus all user data (settings,
  extensions, history) lives in one movable folder. Nothing is written to
  `%APPDATA%`, the registry, or `Program Files`.
- **Always up to date.** Every launch checks for a newer VS Code release and
  offers a one-click update — with a native progress bar, not a terminal.
- **Safe updates with history.** Updates never overwrite in place. The previous
  version is archived (including its data) so you can roll back in seconds.
- **Zero dependencies.** A single ~6 MB EXE. Nothing to install, nothing to
  ship alongside it.

## Download

Grab the latest build — always the same stable link:

**➡ [github.com/jboxberger/codeportable/releases/latest/download/CodePortable.exe](https://github.com/jboxberger/codeportable/releases/latest/download/CodePortable.exe)**

Put `CodePortable.exe` in an empty folder of your choice and run it. That's it.
Each release also ships a `CodePortable.exe.sha256` file so you can verify the
download with `Get-FileHash`.

## How it works

- **First run.** If there is no `current\Code.exe` next to the launcher, it
  downloads the latest VS Code portable archive (Stable, `win32-x64-archive`),
  verifies its SHA-256 checksum, unpacks it into `current\`, creates the
  portable `current\data\` folder, and starts VS Code. Download and extraction
  run inside a native popup with a progress bar.
- **Every launch.** The launcher reads the installed version from the
  `current\Code.exe` version resource and asks the update server whether a
  newer one exists. If so, a dialog offers to update. Offline? VS Code just
  starts normally.
- **Arguments** (e.g. a file or folder to open) are passed straight through to
  `Code.exe`, resolved against your current working directory.

### Version history & one-click revert

Updates never overwrite anything. The new version is built completely in a
staging folder (including a copy of your user data); then the current `current\`
folder — **together with its `data\` folder** — is moved to `old\<version>\`,
and the new build is activated as `current\`. Every previous version is
therefore a complete, self-contained backup.

**Revert:** rename (or delete) `current\`, rename the desired folder from
`old\` back to `current\` — done, including that version's settings.

`old\` keeps only the newest `keepversions` builds (default 5, configurable);
older ones are pruned automatically after an update.

### Cancelable & crash-safe

Download and extraction happen entirely inside the `install.tmp` staging folder
and can be canceled at any time via the Cancel button (or the window's X) — on
cancel, `install.tmp` is removed and the existing installation is left
untouched. The switch-over itself is just two rename operations; renaming the
old `current\` doubles as a lock test and fails while VS Code is still running.
A crash therefore leaves either a leftover `install.tmp` (cleaned up on next
start) or a `current\` without `Code.exe` (triggers a fresh install on next
start). **User data is never deleted** — at worst it sits in the most recent
`old\<version>\` folder.

## Configuration — `config.ini`

On first run the launcher creates a well-commented `config.ini` next to the EXE:

| Key            | Meaning                                                                 | Default |
|----------------|-------------------------------------------------------------------------|---------|
| `apiurl`       | API endpoint used to resolve the latest version and its download URL.   | VS Code Stable, win32-x64-archive |
| `keepversions` | How many previous versions to keep in `old\` (`0` = keep none).         | `5`     |
| `loglevel`     | Minimum severity written to the logfile next to the EXE (`<ExeName>.log`): `error`, `warn`, or `info`. | `warn` |

The logfile is created **only** when a message of at least `loglevel` occurs, so a cleanly working launcher leaves no logfile behind. Set `loglevel = info` for a full step-by-step trace when diagnosing update or startup problems (timeouts, DNS failures, HTTP errors); `error` logs failures only. The file is truncated once it exceeds 512 KB.

## Folder layout

```
CodePortable.exe      <- the launcher
config.ini            <- download source + number of versions to keep
current\              <- active VS Code instance (Code.exe, ...)
current\data\         <- portable data folder (settings, extensions)
old\1.120.0\          <- previous version incl. its data snapshot
old\1.121.0\
install.tmp\          <- staging during download/extract, deleted afterwards
```

## Build from source

Requirements: [Go](https://go.dev/dl/) 1.21+ and Windows (the launcher targets
`GOOS=windows`). Then:

```powershell
.\build.ps1
```

The script embeds the icon (`code.ico`) and the application manifest
(Common Controls v6, DPI-aware) as a Windows resource, then builds a static GUI
EXE with `CGO_ENABLED=0` and `-H=windowsgui`. At runtime it uses only Windows
system libraries (`user32`, `kernel32`, `gdi32`, `comctl32`, `version`) — no
DLLs to ship.

Run the tests with:

```powershell
go test ./...
```

## Source overview

| File            | Purpose                                                       |
|-----------------|---------------------------------------------------------------|
| `main.go`       | Flow control: first run, update check, archiving, launch      |
| `install.go`    | Version query, checksummed download, extraction, copy         |
| `config.go`     | Read `config.ini` (created on first run)                       |
| `progressui.go` | Native Win32 progress window (status, progress bar, cancel)   |
| `winui.go`      | Native message-box dialogs                                    |
| `version.go`    | Read product version from the EXE version resource            |
| `main_test.go`  | Tests for version comparison, pruning, archive naming         |
| `build.ps1`     | Build script incl. icon/manifest embedding                    |

## FAQ

**Nothing happens when I start it — no window opens.**
Visual Studio Code checks a machine-wide mutex (`vscode-updating`) at startup.
If another VS Code installation on the same machine is updating (its silent
Inno-Setup updater holds that lock), *any* VS Code — including the portable one
— refuses to start until the lock is released. Close the other VS Code so its
update can finish; CodePortable detects this situation and shows a clear dialog
with **Retry / Continue anyway / Cancel** instead of silently doing nothing.

**Does it phone home / send telemetry?**
The launcher itself only contacts Microsoft's official VS Code update endpoint
to check for and download new versions. Everything else is Visual Studio Code's
own behavior, configurable inside VS Code.

**Is this an official Microsoft product?**
No. CodePortable is an independent open-source launcher. "Visual Studio Code"
and "VS Code" are products of Microsoft; this project just downloads and runs
the official builds.

## License

[MIT](LICENSE) © Juri Boxberger

Visual Studio Code is downloaded from Microsoft's official distribution servers
under its own license.
