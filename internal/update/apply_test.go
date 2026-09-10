package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// releaseServer serves a whole release: the API reply, the platform binary and
// the checksum list, with the asset URLs pointing back at itself.
type releaseServer struct {
	*httptest.Server
	binary   []byte
	checksum string
	// corruptChecksums makes the list disagree with the binary, which is the
	// case that must refuse to install.
	corruptChecksums bool
	// omitChecksums leaves the list off the release entirely.
	omitChecksums bool
	// omitBinary is a release built for other platforms only.
	omitBinary bool
	tag        string
}

func newReleaseServer(t *testing.T, binary []byte) *releaseServer {
	t.Helper()
	sum := sha256.Sum256(binary)
	rs := &releaseServer{binary: binary, checksum: hex.EncodeToString(sum[:]), tag: "v0.2.0"}

	mux := http.NewServeMux()
	name := runningAssetName()
	rs.Server = httptest.NewServer(mux)

	mux.HandleFunc("/repos/owner/name/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		rel := Release{
			TagName: rs.tag,
			HTMLURL: "https://example.invalid/releases/" + rs.tag,
		}
		if !rs.omitBinary {
			rel.Assets = append(rel.Assets, Asset{
				Name: name, URL: rs.URL + "/download/" + name, Size: int64(len(rs.binary)),
			})
		}
		if !rs.omitChecksums {
			rel.Assets = append(rel.Assets, Asset{
				Name: ChecksumsAsset,
				URL:  rs.URL + "/download/" + ChecksumsAsset,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(rel); err != nil {
			t.Errorf("encode release: %v", err)
		}
	})
	mux.HandleFunc("/download/"+name, func(w http.ResponseWriter, r *http.Request) {
		w.Write(rs.binary)
	})
	mux.HandleFunc("/download/"+ChecksumsAsset, func(w http.ResponseWriter, r *http.Request) {
		sum := rs.checksum
		if rs.corruptChecksums {
			sum = strings.Repeat("0", sha256.Size*2)
		}
		fmt.Fprintf(w, "%s  %s\n", sum, name)
	})
	return rs
}

// installed is a fake install: a binary in a directory of its own, so a swap
// can be observed without touching the test binary.
type installed struct {
	dir string
	exe string
}

func newInstalled(t *testing.T, content string) *installed {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "work")
	if err := os.WriteFile(exe, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	return &installed{dir: dir, exe: exe}
}

// applyChecker builds a checker pointed at the stub release and the fake
// install, and returns the channel the deferred restart reports on.
func applyChecker(t *testing.T, rs *releaseServer, inst *installed, version string) (*Checker, <-chan string) {
	t.Helper()
	// Buffered and read with a timeout, because the restart is deliberately
	// deferred past ApplyUpdate's return: a plain slice would be written by
	// the timer goroutine and read by this one.
	restarted := make(chan string, 1)
	c := New("owner/name", version, nil)
	c.http = rs.Client()
	c.dl = rs.Client()
	c.baseURL = rs.URL
	c.exePath = func() (string, error) { return inst.exe, nil }
	c.restartAfter = time.Millisecond
	c.restartProcess = func(exe string) error {
		restarted <- exe
		return nil
	}
	return c, restarted
}

func TestApplyUpdateInstallsAndRestarts(t *testing.T) {
	rs := newReleaseServer(t, []byte("the new binary"))
	defer rs.Close()
	inst := newInstalled(t, "the old binary")

	c, restarted := applyChecker(t, rs, inst, "v0.1.0")
	if err := c.ApplyUpdate(context.Background()); err != nil {
		t.Fatalf("ApplyUpdate: %v", err)
	}

	got, err := os.ReadFile(inst.exe)
	if err != nil {
		t.Fatalf("read installed binary: %v", err)
	}
	if string(got) != "the new binary" {
		t.Errorf("installed %q, want %q", got, "the new binary")
	}

	info, err := os.Stat(inst.exe)
	if err != nil {
		t.Fatalf("stat installed binary: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("installed mode = %v, want 0755", info.Mode().Perm())
	}

	// Nothing is left beside the binary: no staged download, no .old.
	entries, err := os.ReadDir(inst.dir)
	if err != nil {
		t.Fatalf("read install dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "work" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("install dir holds %v, want just [work]", names)
	}

	// The restart is deferred so ApplyUpdate can return first, so it is waited
	// for rather than asserted on immediately.
	select {
	case exe := <-restarted:
		if exe != inst.exe {
			t.Errorf("restarted %s, want %s", exe, inst.exe)
		}
	case <-time.After(2 * time.Second):
		t.Error("the app was never restarted")
	}
}

func TestApplyUpdateRefusesBadChecksum(t *testing.T) {
	rs := newReleaseServer(t, []byte("the new binary"))
	rs.corruptChecksums = true
	defer rs.Close()
	inst := newInstalled(t, "the old binary")

	c, restarted := applyChecker(t, rs, inst, "v0.1.0")
	err := c.ApplyUpdate(context.Background())
	if err == nil {
		t.Fatal("a download that fails its checksum must not be installed")
	}
	if !strings.Contains(err.Error(), "checksum") {
		t.Errorf("error = %v, want it to name the checksum", err)
	}

	// The running installation is exactly as it was, and nothing was staged.
	got, _ := os.ReadFile(inst.exe)
	if string(got) != "the old binary" {
		t.Errorf("binary is %q, want it untouched", got)
	}
	entries, _ := os.ReadDir(inst.dir)
	if len(entries) != 1 {
		t.Errorf("install dir holds %d files, want 1", len(entries))
	}
	select {
	case exe := <-restarted:
		t.Errorf("restarted %s after a failed update", exe)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestApplyUpdateRefusesWithoutChecksums(t *testing.T) {
	rs := newReleaseServer(t, []byte("the new binary"))
	rs.omitChecksums = true
	defer rs.Close()
	inst := newInstalled(t, "the old binary")

	c, _ := applyChecker(t, rs, inst, "v0.1.0")
	err := c.ApplyUpdate(context.Background())
	if err == nil {
		t.Fatal("a release with no checksum list must not be installed")
	}
	if !strings.Contains(err.Error(), ChecksumsAsset) {
		t.Errorf("error = %v, want it to name %s", err, ChecksumsAsset)
	}
	got, _ := os.ReadFile(inst.exe)
	if string(got) != "the old binary" {
		t.Errorf("binary is %q, want it untouched", got)
	}
}

func TestApplyUpdateRefusesWhenCurrent(t *testing.T) {
	rs := newReleaseServer(t, []byte("the new binary"))
	defer rs.Close()
	inst := newInstalled(t, "the old binary")

	c, _ := applyChecker(t, rs, inst, "v0.2.0")
	err := c.ApplyUpdate(context.Background())
	if err == nil {
		t.Fatal("ApplyUpdate should refuse when the running version is the latest")
	}
	if !strings.Contains(err.Error(), "latest release") {
		t.Errorf("error = %v", err)
	}
}

func TestApplyUpdateRefusesMissingPlatformAsset(t *testing.T) {
	rs := newReleaseServer(t, []byte("the new binary"))
	// A release built for another platform only: the asset this binary needs
	// is absent, which must be a clear error rather than a partial install.
	rs.omitBinary = true
	defer rs.Close()
	inst := newInstalled(t, "the old binary")

	c, _ := applyChecker(t, rs, inst, "v0.1.0")
	err := c.ApplyUpdate(context.Background())
	if err == nil {
		t.Fatal("a release without this platform's asset must fail")
	}
	if !strings.Contains(err.Error(), runningAssetName()) {
		t.Errorf("error = %v, want it to name %s", err, runningAssetName())
	}
}
