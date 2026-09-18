package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// maxAssetBytes bounds a download. The binary is tens of MiB; this is the
	// point past which something has gone wrong and Work should stop writing
	// to the user's disk rather than keep going.
	maxAssetBytes = 256 << 20 // 256 MiB
	// downloadTimeout covers fetching the checksums and the binary together.
	// Generous, because this is a user-initiated install over whatever
	// connection they have, not a background poll.
	downloadTimeout = 10 * time.Minute
	// restartDelay is the gap between the swap succeeding and the process
	// being replaced. It exists so ApplyUpdate can return -- the frontend gets
	// a resolved promise and a chance to paint "restarting" -- before the
	// window goes away underneath it.
	restartDelay = 300 * time.Millisecond
)

// ApplyUpdate installs the latest release over the running binary and restarts
// into it.
//
// The order is deliberate. Everything that can fail -- resolving the release,
// finding the asset for this platform, downloading it, checking it against the
// release's checksums, writing it next to the current binary -- happens before
// anything the user can see changes, and any of those failing leaves the
// installation exactly as it was. Only once a verified binary is in place does
// the swap happen, and the swap itself is a rename, which is atomic: there is
// no window in which the binary on disk is half a Work.
//
// It returns once the new binary is in place. The restart follows a moment
// later; see restartDelay.
func (c *Checker) ApplyUpdate(ctx context.Context) error {
	// A development build is somebody's working tree output. Overwriting it
	// with a release would throw away the thing they are testing.
	if IsDevBuild(c.version) {
		return errors.New("update: this is a development build, so there is nothing to update")
	}

	rel, err := c.latestRelease(ctx)
	if err != nil {
		return err
	}
	if !IsNewer(rel.TagName, c.version) {
		return fmt.Errorf("update: %s is already the latest release", c.version)
	}

	exe, err := c.exePath()
	if err != nil {
		return err
	}

	name := runningAssetName()
	asset, ok := rel.Asset(name)
	if !ok {
		return fmt.Errorf("update: release %s has no %s", rel.TagName, name)
	}
	sums, ok := rel.Asset(ChecksumsAsset)
	if !ok {
		// Without the checksum list there is nothing to verify the download
		// against, and an unverified binary is not something to hand the
		// operating system and then execute.
		return fmt.Errorf("update: release %s has no %s", rel.TagName, ChecksumsAsset)
	}

	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	want, err := c.expectedSum(ctx, sums, name)
	if err != nil {
		return err
	}

	staged, err := c.download(ctx, asset, exe, want)
	if err != nil {
		return err
	}
	// From here the staged file is either installed or removed; it never
	// survives as litter next to the binary.
	if err := swap(staged, exe); err != nil {
		os.Remove(staged)
		return err
	}

	time.AfterFunc(c.restartAfter, func() {
		if err := c.restartProcess(exe); err != nil {
			// The new binary is installed and verified either way, so this is
			// "restart it yourself", not a failed update.
			fmt.Fprintf(os.Stderr, "update: installed %s but could not restart: %v\n", rel.TagName, err)
		}
	})
	return nil
}

// runningBinary is the path to replace: the real file, with any symlink
// resolved, so a launcher symlink is left pointing at a fresh binary rather
// than being overwritten by one.
func runningBinary() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("update: locate running binary: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("update: resolve %s: %w", exe, err)
	}
	return resolved, nil
}

// expectedSum fetches the release's checksum list and returns the SHA-256 the
// named asset must hash to.
func (c *Checker) expectedSum(ctx context.Context, sums Asset, name string) (string, error) {
	body, err := c.get(ctx, sums.URL)
	if err != nil {
		return "", err
	}
	defer body.Close()

	// The list is one short line per asset, so it is read whole rather than
	// scanned: a few hundred bytes.
	data, err := io.ReadAll(io.LimitReader(body, maxAPIBytes))
	if err != nil {
		return "", fmt.Errorf("update: read %s: %w", ChecksumsAsset, err)
	}
	sum, ok := FindChecksum(string(data), name)
	if !ok {
		return "", fmt.Errorf("update: %s does not list %s", ChecksumsAsset, name)
	}
	return sum, nil
}

// FindChecksum reads `sha256sum` output and returns the hash recorded for one
// file. The format is a hex digest, whitespace, then the name, optionally
// prefixed with the binary-mode `*`.
func FindChecksum(list, name string) (string, bool) {
	for _, line := range strings.Split(list, "\n") {
		digest, file, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		file = strings.TrimLeft(file, " *")
		if file != name {
			continue
		}
		if len(digest) != sha256.Size*2 {
			return "", false
		}
		if _, err := hex.DecodeString(digest); err != nil {
			return "", false
		}
		return strings.ToLower(digest), true
	}
	return "", false
}

// download writes the asset to a temporary file beside the running binary and
// returns its path, having checked it hashes to want.
//
// Beside the binary rather than in the system temp directory for one concrete
// reason: the install is a rename, and rename only works within a filesystem.
// Staging in /tmp would mean copying the whole binary a second time on any
// machine where /tmp is its own mount, and would turn the atomic swap into a
// copy the user can interrupt halfway.
//
// The hash is computed from the same bytes as they are written, so verifying
// the download costs no extra read and no extra pass.
func (c *Checker) download(ctx context.Context, asset Asset, exe, want string) (string, error) {
	body, err := c.get(ctx, asset.URL)
	if err != nil {
		return "", err
	}
	defer body.Close()

	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(exe)+".update-*")
	if err != nil {
		// A binary installed somewhere the user cannot write -- /usr/bin from
		// a package -- lands here, and the package manager is the right way to
		// update that one.
		return "", fmt.Errorf("update: stage download in %s: %w", dir, err)
	}
	staged := tmp.Name()

	digest := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(tmp, digest), io.LimitReader(body, maxAssetBytes))
	closeErr := tmp.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		os.Remove(staged)
		return "", fmt.Errorf("update: download %s: %w", asset.Name, err)
	}

	if got := hex.EncodeToString(digest.Sum(nil)); got != want {
		os.Remove(staged)
		return "", fmt.Errorf("update: %s failed its checksum: expected %s, got %s", asset.Name, want, got)
	}

	// The mode of the binary being replaced, not a hardcoded one, so an
	// install that is group-executable or setgid keeps being that.
	mode := os.FileMode(0o755)
	if info, err := os.Stat(exe); err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.Chmod(staged, mode); err != nil {
		os.Remove(staged)
		return "", fmt.Errorf("update: make %s executable: %w", staged, err)
	}
	return staged, nil
}

// swap puts the staged binary in place of the running one.
//
// The current binary is moved aside first rather than being overwritten. On
// Unix that buys a rollback if the second rename fails; on Windows it is the
// only thing that works at all, because a running image cannot be replaced but
// can be renamed.
func swap(staged, exe string) error {
	aside := exe + ".old"
	os.Remove(aside)
	if err := os.Rename(exe, aside); err != nil {
		return fmt.Errorf("update: move %s aside: %w", exe, err)
	}
	if err := os.Rename(staged, exe); err != nil {
		// Put the old binary back, so a failed update leaves a working app
		// rather than no app.
		if restoreErr := os.Rename(aside, exe); restoreErr != nil {
			return fmt.Errorf("update: install %s failed (%w) and %s could not be restored: %w", exe, err, aside, restoreErr)
		}
		return fmt.Errorf("update: install %s: %w", exe, err)
	}
	// Best effort: the old binary is still mapped by the running process on
	// some platforms, and leaving one stale file beside the new one is a much
	// smaller problem than failing an update that has already succeeded.
	os.Remove(aside)
	return nil
}

// get performs a GET and hands back the body for the caller to close. Release
// asset URLs redirect to a CDN, which http.Client follows.
func (c *Checker) get(ctx context.Context, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("update: build request: %w", err)
	}
	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("User-Agent", "work/"+c.version)

	// Downloads are large and slow, so they do not share the poller's client
	// timeout: the context governs them instead.
	resp, err := c.dl.Do(req)
	if err != nil {
		return nil, fmt.Errorf("update: download %s: %w", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("update: download %s: %s", url, resp.Status)
	}
	return resp.Body, nil
}
