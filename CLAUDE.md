# CLAUDE.md

Guidance for Claude Code when working in this repository.

## Overview

**Paperclip** — a Go menu-bar/tray daemon that syncs the clipboard (text and
images) between machines through [Ably](https://ably.com) pub/sub. All payloads
are encrypted on-device with AES-256-GCM before publishing; the relay never
sees plaintext.

- Module: `github.com/mindmorass/paperclip` (repo: `neverprepared/paperclip`)
- Go 1.24.4. Version string lives in `main.go` (`var version`).
- **Supported OS: macOS and Windows only.** There is no Linux clipboard
  implementation, so `go build ./...` on Linux fails in `ui`/`clipboard`.
  Cross-check work with `GOOS=windows go build ./...` or build on a Mac.
- This project exposes **no MCP server, no HTTP API, and no library surface**
  intended for external consumers. Its only interface is the `paperclip` binary
  (CLI flags + tray menu).

## Architecture

```
main.go            flag parsing, config load, key resolution, tray vs daemon
├── config/        JSON config in os.UserConfigDir()/Paperclip/config.json
├── clipboard/     OS clipboard read/write, SHA-256 change detection
├── relay/         Ably transport, crypto, keychain access
├── ui/            systray menu, native prompts, jiggler, login item
└── update/        GitHub latest-release check + open browser
```

- **`main.go`** — resolves the Ably API key (keychain → `PAPERCLIP_ABLY_KEY`
  env fallback), builds the relay via `startRelay`, then runs either
  `runTray` (systray UI) or `runDaemon` (headless, blocks on SIGINT/SIGTERM).
  Tray mode is selected by `--tray` **or** by an argv[0] containing `tray`
  (so `paperclip-tray.exe` works on double-click).
- **`config`** — `Config{PollMs, Verbose, ClearAfterSeconds, JiggleMode,
  IsHub, HubTargets, Relay{Clipboards[]}}`. `Validate()` runs after load *and*
  after CLI overrides. Secrets are never written here.
- **`clipboard`** — `Clipboard.Read()/Write()` are per-OS
  (`clipboard_darwin.go` drives NSPasteboard through `osascript`/AppleScript-ObjC,
  base64-framing the payload to dodge `pbpaste`/`pbcopy` encoding issues;
  `clipboard_windows.go` uses Win32 syscalls). `Content{Type, Data, Hash}`, types `TypeText` (0x01)
  and `TypeImage` (0x02). Images are capped at 16 MB on read.
- **`relay`** — `Relay` subscribes one Ably channel per clipboard "room" and
  runs `pollAndPublish` on a ticker. Wire format `ablyMsg{t,d,s,m}`:
  type, base64 ciphertext, random per-session sender ID (echo suppression),
  and hex `HMAC-SHA256(encKey, "t:d:s")`. Hub mode: `SetPublishFilter` limits
  which rooms this node publishes to (receives from all).
- **`ui`** — `ui.Run` builds the systray menu (`trayState.build`, `tray.go`).
  Native dialogs are per-OS (`prompt_darwin.go` shells `osascript`;
  `prompt_windows.go`). `Jiggler` moves the mouse (`minimal` / `natural`
  modes) to prevent screen sleep. Login item = LaunchAgent on macOS,
  `HKCU\...\CurrentVersion\Run` on Windows.

### Crypto invariants — do not weaken

- Encryption is **mandatory**. A room without a passphrase is skipped at
  startup and never published to.
- Key = Argon2id(passphrase, salt=SHA-256("paperclip:"+room), t=2, m=64MB,
  p=4, 32 bytes) — `relay/encrypt.go`.
- AES-256-GCM with the **room name as AAD** (binds ciphertext to its room).
- An 8-byte big-endian Unix timestamp is prepended *inside* the AEAD envelope;
  receivers reject anything outside a ±5 minute window (`replayWindowSeconds`).
- Plaintext is capped at `maxPlaintextBytes` (47 KB) so the serialised message
  stays under Ably's 64 KB hard limit (`ablyMessageSizeLimit`).
- The message hash is deliberately **not** on the wire (it would leak content
  identity); echo prevention uses the sender ID.
- Secrets live only in the OS credential store (`relay/keychain.go`, service
  `com.github.mindmorass.paperclip`): item `ably-api-key`, and
  `clipboard:<name>` per room. Never log or persist them.

## Key commands

```bash
make build              # CGO_ENABLED=1 go build -o paperclip .   (host/macOS)
make app                # macOS .app bundle (LSUIElement, no dock icon)
make install            # build + copy to $INSTALL_PATH (default ~/bin)
make build-windows      # paperclip.exe       (console daemon)
make build-windows-tray # paperclip-tray.exe  (-H windowsgui, no console)
make clean

go test ./...           # unit tests: config/ and relay/ only
go vet ./...            # use GOOS=windows/darwin; plain vet fails on Linux
gofmt -l .              # no golangci-lint config; there is NO `make lint`
```

Run:

```bash
paperclip --tray                      # menu bar / tray UI
paperclip --clipboard room1,room2     # daemon, join named clipboards
paperclip --poll 250 -v               # poll interval (ms) + verbose
paperclip --version
PAPERCLIP_ABLY_KEY=key:secret paperclip --clipboard myroom   # env fallback
```

## CI

`.github/workflows/build.yml` — builds darwin/arm64 (codesign + notarize on
tags) and windows amd64/386, uploads artifacts, and cuts a GitHub release on
`v*` tags. **CI does not run `go test`, `go vet`, or a linter** — run tests
locally before pushing.

## Conventions

- Test coverage exists for `config` and `relay` (crypto, MAC, replay, size
  limits). `relay.New` takes the `clipboardSyncer` interface so tests can
  inject a fake clipboard — keep it that way; don't depend on `*clipboard.Clipboard`.
- Platform-specific code goes in `*_darwin.go` / `*_windows.go` with an
  `//go:build !darwin && !windows` no-op `*_other.go` stub so the package
  still type-checks elsewhere.
- Room names are validated against `^[a-zA-Z0-9_-]+$` (`ui/tray.go`).
  Passphrases must be ≥ 8 chars; Ably keys must look like `key:secret` (≥20 chars).
- Keep the relay non-fatal: log and continue on per-room errors rather than
  killing the process.
- Commits: concise, imperative. One logical change per commit.
