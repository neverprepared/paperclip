# Paperclip

A peer-to-peer clipboard synchronization tool for macOS. Automatically syncs clipboard content (text and images) between multiple machines over TCP.

## Features

- Syncs text and images between peers
- Supports multiple addresses per peer (e.g., LAN + Tailscale)
- Automatic reconnection with exponential backoff
- Echo prevention to avoid clipboard loops
- Runs as a background service via launchd

## Installation

```bash
go build -o paperclip .
```

Optionally, install to your PATH:

```bash
cp paperclip ~/bin/
```

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
| `-launchd` | false | Generate and install launchd plist |

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

## Running as a Service (launchd)

### Generate and Install the plist

```bash
./paperclip -launchd -port 9999 -peers "peer1:9999,peer2:9999"
```

This writes the plist file to `~/Library/LaunchAgents/com.github.mindmorass.paperclip.plist`.

### Load the Service

```bash
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.github.mindmorass.paperclip.plist
```

### Unload the Service

```bash
launchctl bootout gui/$(id -u)/com.github.mindmorass.paperclip
```

### Reload After Config Changes

```bash
launchctl bootout gui/$(id -u)/com.github.mindmorass.paperclip
./paperclip -launchd -port 9999 -peers "updated-peers:9999"
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.github.mindmorass.paperclip.plist
```

### View Logs

```bash
# Standard output
tail -f ~/Library/Logs/paperclip.log

# Standard error
tail -f ~/Library/Logs/paperclip.err
```

### Check Service Status

```bash
launchctl list | grep paperclip
```

## Example Setup

### Two Machines (A and B)

On Machine A:
```bash
./paperclip -v -port 9999 -peers "machine-b.local:9999"
```

On Machine B:
```bash
./paperclip -v -port 9999 -peers "machine-a.local:9999"
```

Copy something to the clipboard on either machine - it will automatically appear on the other.

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
