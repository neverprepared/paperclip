package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const repoAPI = "https://api.github.com/repos/neverprepared/paperclip/releases/latest"

// Release holds the version and HTML release page URL for the latest release.
type Release struct {
	Version string // e.g. "0.4.4" (no "v" prefix)
	URL     string // https://github.com/.../releases/tag/v0.4.4
}

type githubRelease struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

// LatestRelease fetches the latest GitHub release metadata.
func LatestRelease(ctx context.Context, currentVersion string) (*Release, error) {
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

	return &Release{
		Version: strings.TrimPrefix(gr.TagName, "v"),
		URL:     gr.HTMLURL,
	}, nil
}

// IsNewer reports whether remote is a higher version than current.
func IsNewer(current, remote string) bool {
	return cmpVersion(remote, current) > 0
}

// OpenBrowser opens the given URL in the default system browser.
func OpenBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
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
