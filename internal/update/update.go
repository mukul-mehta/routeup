// Package update checks for and applies routeup releases. All network access
// is explicit (triggered by `routeup update`); routeup never checks for
// updates on its own.
package update

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// Channel is how the running binary was installed.
type Channel int

const (
	ChannelDirect   Channel = iota // curl installer or manual download
	ChannelHomebrew                // brew install
)

func (c Channel) String() string {
	if c == ChannelHomebrew {
		return "homebrew"
	}
	return "direct"
}

// DetectChannel guesses the install channel from the resolved binary path.
// Pass the EvalSymlinks'd path so a Homebrew bin symlink resolves into the
// Cellar (where the "/Cellar/" marker lives on every platform).
func DetectChannel(resolvedExecPath string) Channel {
	p := strings.ToLower(resolvedExecPath)
	if strings.Contains(p, "/cellar/") || strings.Contains(p, "/homebrew/") {
		return ChannelHomebrew
	}
	return ChannelDirect
}

// Latest returns the latest release tag (e.g. "v0.2.0") for repo (owner/name).
func Latest(ctx context.Context, repo string) (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("query github releases: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("no published releases for %s yet", repo)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github releases returned %s", resp.Status)
	}
	var body struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decode release: %w", err)
	}
	if body.TagName == "" {
		return "", fmt.Errorf("latest release has no tag_name")
	}
	return body.TagName, nil
}

// IsNewer reports whether latestTag is a newer version than current. Both may
// carry a leading v or not; invalid semver returns an error.
func IsNewer(current, latestTag string) (bool, error) {
	cur := ensureV(current)
	lat := ensureV(latestTag)
	if !semver.IsValid(cur) {
		return false, fmt.Errorf("current version %q is not valid semver", current)
	}
	if !semver.IsValid(lat) {
		return false, fmt.Errorf("latest tag %q is not valid semver", latestTag)
	}
	return semver.Compare(cur, lat) < 0, nil
}

func ensureV(v string) string {
	if strings.HasPrefix(v, "v") {
		return v
	}
	return "v" + v
}

// Apply downloads the release asset for tag matching the current os/arch,
// verifies its checksum, and atomically replaces the binary at destPath.
func Apply(ctx context.Context, repo, tag, destPath string) error {
	asset := fmt.Sprintf("routeup_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	base := fmt.Sprintf("https://github.com/%s/releases/download/%s", repo, tag)

	tmp, err := os.MkdirTemp("", "routeup-update-")
	if err != nil {
		return fmt.Errorf("temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	archivePath := filepath.Join(tmp, asset)
	if err := download(ctx, base+"/"+asset, archivePath); err != nil {
		return fmt.Errorf("download %s: %w", asset, err)
	}
	sumsPath := filepath.Join(tmp, "checksums.txt")
	if err := download(ctx, base+"/checksums.txt", sumsPath); err != nil {
		return fmt.Errorf("download checksums: %w", err)
	}
	if err := verifyChecksum(archivePath, sumsPath, asset); err != nil {
		return err
	}

	binPath, err := extractBinary(archivePath, tmp)
	if err != nil {
		return err
	}
	return replaceBinary(binPath, destPath)
}

func download(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = io.Copy(f, resp.Body)
	return err
}

func verifyChecksum(archivePath, sumsPath, assetName string) error {
	sums, err := os.ReadFile(sumsPath)
	if err != nil {
		return err
	}
	var want string
	for _, line := range strings.Split(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == assetName {
			want = fields[0]
			break
		}
	}
	if want == "" {
		return fmt.Errorf("no checksum for %s in checksums.txt", assetName)
	}

	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("checksum mismatch for %s (want %s, got %s)", assetName, want, got)
	}
	return nil
}

// extractBinary pulls the "routeup" file from the tar.gz into dir.
func extractBinary(archivePath, dir string) (string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", err
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		if filepath.Base(hdr.Name) != "routeup" {
			continue
		}
		out := filepath.Join(dir, "routeup.new")
		w, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(w, tr); err != nil {
			_ = w.Close()
			return "", err
		}
		if err := w.Close(); err != nil {
			return "", err
		}
		return out, nil
	}
	return "", fmt.Errorf("routeup binary not found in archive")
}

// replaceBinary atomically swaps destPath with newPath. Safe on a running
// binary on Unix: rename swaps the inode, the running process keeps the old
// one, future execs get the new file. Requires a writable destination dir.
func replaceBinary(newPath, destPath string) error {
	dir := filepath.Dir(destPath)
	staged := filepath.Join(dir, ".routeup.update")
	if err := copyFile(newPath, staged, 0o755); err != nil {
		return fmt.Errorf("stage update in %s (is the directory writable?): %w", dir, err)
	}
	if err := os.Rename(staged, destPath); err != nil {
		_ = os.Remove(staged)
		return fmt.Errorf("replace %s: %w", destPath, err)
	}
	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
