# Paperclip

Sync your clipboard between Macs automatically. Copy on one machine, paste on another — text and images, end-to-end encrypted.

## How it works

Paperclip uses [Ably](https://ably.com) as a pub/sub relay. Every message is encrypted with AES-256-GCM **on-device** before it leaves — Ably never sees plaintext. The passphrase you set is used to derive the encryption key via Argon2id, so all machines sharing a clipboard must use the same passphrase.

## Requirements

- macOS or Windows (no administrator access required)
- A free [Ably account](https://ably.com) — the free tier is sufficient
- Go 1.24+ (to build from source)

## Installation

### macOS — from source

```bash
git clone https://github.com/mindmorass/paperclip
cd paperclip
make install   # builds and copies to ~/bin
```

### macOS — .app bundle (menu bar, no dock icon)

```bash
make app
# Drag Paperclip.app to /Applications
```

### Windows — from source

```bash
# Tray mode (no console window — recommended for desktop use)
make build-windows-tray   # produces paperclip-tray.exe

# Daemon/headless mode (console window for log output)
make build-windows        # produces paperclip.exe
```

> Cross-compiling from macOS/Linux requires no additional tools — `CGO_ENABLED=0` is set automatically.

## First-time setup

Run with the tray UI:

```bash
# macOS
paperclip --tray

# Windows (use the no-console build to avoid a terminal window)
paperclip-tray.exe --tray
```

Click the menubar icon → **Configure Paperclip...** and follow the prompts:

1. Enter your Ably API key (from the Ably dashboard — publish/subscribe permissions required)
2. Name your clipboard, e.g. `home` or `work` — use the **same name** on all machines
3. Set a passphrase — all machines sharing this clipboard must use the **same passphrase**

Repeat on each machine. Paperclip will start syncing within one poll interval (default 500ms).

## Running at login (background service)

From the tray menu: **Settings → Install Login Item**

- **macOS**: installs a LaunchAgent (`~/Library/LaunchAgents/`). Logs → `~/Library/Logs/paperclip.log`.
- **Windows**: writes a value to `HKEY_CURRENT_USER\Software\Microsoft\Windows\CurrentVersion\Run`. No administrator access required.

## CLI / daemon mode

Useful for scripting or headless machines. The Ably API key is read from the system credential store (macOS Keychain / Windows Credential Manager), or from the `PAPERCLIP_ABLY_KEY` environment variable as a fallback.

```bash
paperclip --clipboard myroom
paperclip --clipboard room1,room2   # join multiple clipboards
paperclip --poll 250 -v             # 250ms poll interval, verbose logging
```

Passphrases must be stored in the credential store (via the tray UI, or `cmdkey` on Windows) before running in daemon mode.

**Windows — pre-store credentials without the tray UI:**
```powershell
cmdkey /add:com.github.mindmorass.paperclip /user:ably-api-key /pass:YOUR_KEY
cmdkey /add:com.github.mindmorass.paperclip /user:clipboard:myroom /pass:YOUR_PASSPHRASE
paperclip.exe --clipboard myroom
```

## Hub-spoke mode

One machine can act as a **hub** that receives from all clipboards but only broadcasts to selected ones. Enable **Hub Mode** in the tray menu and choose destinations under **Broadcast to...**.

Use case: a shared server clipboard that only pushes to specific client machines.

## Auto-clear

Wipe the clipboard automatically after a period of inactivity. Configure in the tray under **Settings → Auto-clear Clipboard** (5–60 seconds).

## Security

- Encryption is **mandatory** — clipboards without a passphrase are refused at startup
- **AES-256-GCM** with Argon2id key derivation (t=2, m=64MB, p=4)
- **HMAC-SHA256** on every message; tampered or injected messages are silently dropped
- **Replay protection** — each message contains an 8-byte timestamp inside the AEAD envelope; messages outside a ±5-minute window are rejected
- The Ably API key and all passphrases are stored in the **macOS Keychain** or **Windows Credential Manager** — never written to disk in config files

## License

MIT
