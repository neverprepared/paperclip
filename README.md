# Paperclip

A peer-to-peer clipboard synchronization tool for macOS and Windows. Automatically syncs clipboard content (text and images) between multiple machines over TCP.

## Features

- Cross-platform: macOS and Windows support
- Syncs text and images between peers
- Supports multiple addresses per peer (e.g., LAN + Tailscale)
- Automatic reconnection with exponential backoff
- Echo prevention to avoid clipboard loops
- Runs as a background service (launchd on macOS, Task Scheduler on Windows)
- Zero external dependencies

## Installation

### From Source

```bash
# macOS
go build -o paperclip .

# Windows
GOOS=windows GOARCH=amd64 go build -o paperclip.exe .
```

### From Releases

Download the latest binary from the [Releases](https://github.com/mindmorass/paperclip/releases) page.

## Usage

### Basic Usage

```bash
# Start with default port (9999) and no peers
./paperclip

# Start on a specific port with peers
./paperclip -port 9999 -peers "192.168.1.100:9999,192.168.1.101:9999"

# Enable verbose logging
./paperclip -v -port 9999 -peers "192.168.1.100:9999"
```

### Command Line Options

| Flag | Default | Description |
|------|---------|-------------|
| `-port` | 9999 | TCP port for peer connections |
| `-peers` | "" | Comma-separated list of peer addresses |
| `-poll` | 500 | Clipboard poll interval in milliseconds |
| `-v` | false | Enable verbose logging |
| `-version` | false | Show version |
| `-service` | false | Generate and install platform service config |

### Peer Address Format

Peers are specified as comma-separated addresses. You can also use pipe (`|`) to specify multiple addresses for the same peer (useful when a machine is reachable via multiple networks):

```bash
# Two separate peers
-peers "machine1:9999,machine2:9999"

# One peer reachable via LAN or Tailscale
-peers "192.168.1.100:9999|100.64.0.5:9999"

# Mixed: one peer with multiple addresses, another with single
-peers "192.168.1.100:9999|100.64.0.5:9999,other-machine:9999"
```

## Running as a Service

### macOS (launchd)

#### Generate and Install

```bash
./paperclip -service -port 9999 -peers "peer1:9999,peer2:9999"
```

This writes the plist file to `~/Library/LaunchAgents/com.github.mindmorass.paperclip.plist`.

#### Load the Service

```bash
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.github.mindmorass.paperclip.plist
```

#### Unload the Service

```bash
launchctl bootout gui/$(id -u)/com.github.mindmorass.paperclip
```

#### Reload After Config Changes

```bash
launchctl bootout gui/$(id -u)/com.github.mindmorass.paperclip
./paperclip -service -port 9999 -peers "updated-peers:9999"
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.github.mindmorass.paperclip.plist
```

#### View Logs

```bash
tail -f ~/Library/Logs/paperclip.log
tail -f ~/Library/Logs/paperclip.err
```

#### Check Service Status

```bash
launchctl list | grep paperclip
```

### Windows (Task Scheduler)

#### Generate and Install

```powershell
.\paperclip.exe -service -port 9999 -peers "peer1:9999,peer2:9999"
```

This creates a scheduled task named "Paperclip" that runs at user logon.

**Note:** No administrator privileges are required for per-user tasks.

#### Start Immediately

```powershell
schtasks /Run /TN Paperclip
```

#### Stop the Service

```powershell
schtasks /End /TN Paperclip
```

#### Check Status

```powershell
schtasks /Query /TN Paperclip
```

#### Remove the Service

```powershell
schtasks /Delete /TN Paperclip /F
```

#### Update Configuration

```powershell
# Remove and recreate with new settings
schtasks /Delete /TN Paperclip /F
.\paperclip.exe -service -port 9999 -peers "updated-peers:9999"
schtasks /Run /TN Paperclip
```

## Example Setup

### Two Machines (A and B)

On Machine A (macOS):
```bash
./paperclip -v -port 9999 -peers "machine-b.local:9999"
```

On Machine B (Windows):
```powershell
.\paperclip.exe -v -port 9999 -peers "machine-a.local:9999"
```

Copy something to the clipboard on either machine - it will automatically appear on the other.

### Cross-Platform Network

```
macOS Laptop  <---->  Windows Desktop  <---->  macOS Mini
     :9999                 :9999                  :9999
```

Each machine connects to the others. The mesh topology ensures clipboard content propagates to all peers.

## Platform Notes

### macOS
- Uses `pbpaste`/`pbcopy` for text
- Uses AppleScript with NSPasteboard for images (PNG)
- Images are converted to PNG for cross-platform compatibility

### Windows
- Uses Win32 clipboard APIs via syscalls (no CGO required)
- Supports CF_UNICODETEXT for text (UTF-16LE)
- Supports CF_PNG and CF_DIB for images
- Task Scheduler runs in user session (required for clipboard access)

## Building

### macOS (Apple Silicon)

```bash
CGO_ENABLED=0 go build -ldflags="-s -w" -o paperclip .
```

### Windows (64-bit)

```bash
GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o paperclip.exe .
```

### Windows (32-bit)

```bash
GOOS=windows GOARCH=386 go build -ldflags="-s -w" -o paperclip.exe .
```

## GitHub Actions

The project includes a GitHub Actions workflow that builds, signs, and notarizes macOS binaries for Apple Silicon (arm64).

### Required Secrets

Configure these secrets in your GitHub repository settings:

| Secret | Description |
|--------|-------------|
| `MACOS_CERTIFICATE` | Base64-encoded .p12 certificate file |
| `MACOS_CERTIFICATE_PWD` | Password for the .p12 certificate |
| `MACOS_IDENTITY` | Signing identity (e.g., `Developer ID Application: Your Name (TEAMID)`) |
| `KEYCHAIN_PWD` | Password for the temporary keychain |
| `APPLE_ID` | Your Apple ID email |
| `APPLE_TEAM_ID` | Your Apple Developer Team ID |
| `APPLE_APP_PASSWORD` | App-specific password for notarization |

### Exporting Your Certificate

```bash
# Export from Keychain Access as .p12, then:
base64 -i certificate.p12 | pbcopy
# Paste the result as MACOS_CERTIFICATE secret
```

### Creating an App-Specific Password

1. Go to https://appleid.apple.com/
2. Sign in and go to Security → App-Specific Passwords
3. Generate a new password for "GitHub Actions"

## License

MIT
