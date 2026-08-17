// Package update checks GitHub for a newer WhatTheDock release and, when
// asked, downloads and installs it in place of the currently running
// binary. It mirrors two things that already exist elsewhere in this repo
// rather than inventing new conventions: the release asset naming
// (cmd/release's releaseBinaryName) and the download flow
// (scripts/install.sh) — both are whatthedock-<version>-<os>-<arch>
// fetched from a GitHub release's assets.
package update

import (
	"context"
	"encoding/json"
	"errors"
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

// Repo is the GitHub "owner/name" WhatTheDock releases are published under.
const Repo = "allisonhere/whatthedock"

var httpClient = &http.Client{Timeout: 15 * time.Second}

// executableOverride is os.Executable behind a seam so tests can point
// ReplaceRunningExecutable at a throwaway file instead of the real test
// binary's own path.
var executableOverride = os.Executable

// release is the subset of GitHub's release API response this package
// needs.
type release struct {
	TagName string `json:"tag_name"`
}

// LatestRelease fetches repo's latest published release tag (e.g.
// "v0.1.4") from the GitHub API.
func LatestRelease(ctx context.Context, repo string) (string, error) {
	url := "https://api.github.com/repos/" + repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github: unexpected status %s", resp.Status)
	}
	var rel release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", err
	}
	if strings.TrimSpace(rel.TagName) == "" {
		return "", errors.New("github: release response had no tag")
	}
	return rel.TagName, nil
}

// IsNewer reports whether latest is a strictly newer vMAJOR.MINOR.PATCH
// version than current. Either string failing to parse in that shape
// (notably "dev", the ldflags default for a non-release build) always
// reports false — there's no sane "current" to compare against, so it's
// treated as never eligible for an update prompt rather than guessed at.
func IsNewer(current, latest string) bool {
	cMaj, cMin, cPatch, ok := parseVersion(current)
	if !ok {
		return false
	}
	lMaj, lMin, lPatch, ok := parseVersion(latest)
	if !ok {
		return false
	}
	if lMaj != cMaj {
		return lMaj > cMaj
	}
	if lMin != cMin {
		return lMin > cMin
	}
	return lPatch > cPatch
}

func parseVersion(v string) (major, minor, patch int, ok bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return 0, 0, 0, false
	}
	nums := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return 0, 0, 0, false
		}
		nums[i] = n
	}
	return nums[0], nums[1], nums[2], true
}

// AssetName is the release asset filename for version on the current
// platform — must match cmd/release's releaseBinaryName exactly, since
// that's what actually names the uploaded asset.
func AssetName(version string) string {
	return fmt.Sprintf("whatthedock-%s-%s-%s", version, runtime.GOOS, runtime.GOARCH)
}

// DownloadURL is the GitHub release asset URL for version.
func DownloadURL(repo, version string) string {
	return fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", repo, version, AssetName(version))
}

// download fetches url's body into a new file in dir (so the caller's
// later rename is same-filesystem, and therefore atomic), returning its
// path. The caller owns cleaning it up on error.
func download(ctx context.Context, url, dir string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: unexpected status %s", url, resp.Status)
	}
	tmp, err := os.CreateTemp(dir, "whatthedock-update-*")
	if err != nil {
		return "", err
	}
	name := tmp.Name()
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		os.Remove(name)
		return "", err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		os.Remove(name)
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return "", err
	}
	return name, nil
}

// ReplaceRunningExecutable downloads version's release asset for repo and
// atomically replaces the currently running binary with it. It returns the
// executable's path so the caller can re-exec into it — this package never
// does that itself, since replacing a running process image is a
// main-package/process-lifecycle concern, not something a library package
// should reach for.
func ReplaceRunningExecutable(ctx context.Context, repo, version string) (string, error) {
	exe, err := executableOverride()
	if err != nil {
		return "", err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", err
	}
	tmp, err := download(ctx, DownloadURL(repo, version), filepath.Dir(exe))
	if err != nil {
		return "", err
	}
	if err := os.Rename(tmp, exe); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("install %s over %s: %w", tmp, exe, err)
	}
	return exe, nil
}
