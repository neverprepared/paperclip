package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const repoAPI = "https://api.github.com/repos/neverprepared/paperclip/releases/latest"

// Release holds the version and download URL for the matching platform asset.
type Release struct {
	Version     string // e.g. "0.4.3" (no "v" prefix)
	DownloadURL string
	AssetName   string
}

type githubRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// LatestRelease fetches the latest GitHub release and returns the asset
// matching the current OS/arch. Returns an error if no matching asset exists.
func LatestRelease(ctx context.Context, currentVersion string) (*Release, error) {
	asset, err := platformAsset()
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, repoAPI, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "paperclip/"+currentVersion)
	req.Header.Set("Accept", "application/vnd.github+json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("release check failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("no releases found")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned %s", resp.Status)
	}

	var gr githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&gr); err != nil {
		return nil, fmt.Errorf("failed to parse release: %w", err)
	}

	version := strings.TrimPrefix(gr.TagName, "v")

	for _, a := range gr.Assets {
		if a.Name == asset {
			return &Release{
				Version:     version,
				DownloadURL: a.BrowserDownloadURL,
				AssetName:   asset,
			}, nil
		}
	}

	return nil, fmt.Errorf("release %s has no binary for %s/%s", gr.TagName, runtime.GOOS, runtime.GOARCH)
}

// IsNewer reports whether remote is a higher version than current.
func IsNewer(current, remote string) bool {
	return cmpVersion(remote, current) > 0
}

// Download fetches the release binary into ~/Downloads and returns the saved path.
func Download(ctx context.Context, rel *Release, currentVersion string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	dest := filepath.Join(home, "Downloads", rel.AssetName)
	tmp := dest + ".tmp"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rel.DownloadURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "paperclip/"+currentVersion)

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download returned %s", resp.Status)
	}

	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return "", fmt.Errorf("cannot create file: %w", err)
	}

	_, copyErr := io.Copy(f, resp.Body)
	f.Close()
	if copyErr != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("download interrupted: %w", copyErr)
	}

	if err := os.Rename(tmp, dest); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("failed to save download: %w", err)
	}

	return dest, nil
}

// platformAsset returns the GitHub release asset filename for the current platform.
func platformAsset() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		if runtime.GOARCH == "arm64" {
			return "paperclip-darwin-arm64", nil
		}
	case "windows":
		exe := strings.ToLower(filepath.Base(os.Args[0]))
		switch runtime.GOARCH {
		case "amd64":
			if strings.Contains(exe, "tray") {
				return "paperclip-windows-tray-amd64.exe", nil
			}
			return "paperclip-windows-amd64.exe", nil
		case "386":
			return "paperclip-windows-386.exe", nil
		}
	}
	return "", fmt.Errorf("no release binary available for %s/%s", runtime.GOOS, runtime.GOARCH)
}

// cmpVersion compares two semver strings (without "v" prefix).
// Returns 1 if a > b, -1 if a < b, 0 if equal.
func cmpVersion(a, b string) int {
	ap := splitVersion(a)
	bp := splitVersion(b)
	for i := 0; i < 3; i++ {
		if ap[i] != bp[i] {
			if ap[i] > bp[i] {
				return 1
			}
			return -1
		}
	}
	return 0
}

func splitVersion(v string) [3]int {
	parts := strings.SplitN(strings.TrimPrefix(v, "v"), ".", 3)
	var out [3]int
	for i, p := range parts {
		if i >= 3 {
			break
		}
		out[i], _ = strconv.Atoi(p)
	}
	return out
}
