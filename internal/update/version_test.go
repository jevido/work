package update

import "testing"

func TestCompare(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"v1.0.0", "v1.0.0", 0},
		{"1.0.0", "v1.0.0", 0},
		{"v1.2", "v1.2.0", 0},
		{"v1.0.1", "v1.0.0", 1},
		{"v1.0.0", "v1.0.1", -1},
		// The case a string compare gets wrong, which is the whole reason this
		// function exists.
		{"v0.10.0", "v0.9.0", 1},
		{"v0.9.0", "v0.10.0", -1},
		{"v2.0.0", "v1.99.99", 1},
		// A prerelease leads to the release, so it sorts before it.
		{"v1.0.0-rc.1", "v1.0.0", -1},
		{"v1.0.0", "v1.0.0-rc.1", 1},
		{"v1.0.0-rc.2", "v1.0.0-rc.1", 1},
		// Build metadata does not affect ordering.
		{"v1.0.0+abc", "v1.0.0", 0},
		// Unparseable fields read as zero rather than failing.
		{"vx.y.z", "v0.0.0", 0},
		{"v1.0.0", "", 1},
	}
	for _, tt := range tests {
		if got := Compare(tt.a, tt.b); got != tt.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestIsNewer(t *testing.T) {
	if !IsNewer("v0.2.0", "v0.1.9") {
		t.Error("v0.2.0 should be newer than v0.1.9")
	}
	if IsNewer("v0.1.0", "v0.1.0") {
		t.Error("a release is not newer than itself")
	}
	if IsNewer("v0.1.0", "v0.2.0") {
		t.Error("an older release must not read as newer")
	}
}

func TestIsDevBuild(t *testing.T) {
	for _, v := range []string{"", "dev", "0.0.0", "  "} {
		if !IsDevBuild(v) {
			t.Errorf("IsDevBuild(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"v0.0.1", "0.1.0"} {
		if IsDevBuild(v) {
			t.Errorf("IsDevBuild(%q) = true, want false", v)
		}
	}
}

func TestAssetName(t *testing.T) {
	tests := []struct {
		goos, goarch, want string
	}{
		{"linux", "amd64", "work-linux-amd64"},
		{"linux", "arm64", "work-linux-arm64"},
		{"darwin", "arm64", "work-darwin-arm64"},
		{"windows", "amd64", "work-windows-amd64.exe"},
	}
	for _, tt := range tests {
		if got := AssetName(tt.goos, tt.goarch); got != tt.want {
			t.Errorf("AssetName(%q, %q) = %q, want %q", tt.goos, tt.goarch, got, tt.want)
		}
	}
}

func TestFindChecksum(t *testing.T) {
	const sum = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	list := "0000000000000000000000000000000000000000000000000000000000000000  work-linux-arm64\n" +
		sum + "  work-linux-amd64\n"

	got, ok := FindChecksum(list, "work-linux-amd64")
	if !ok || got != sum {
		t.Fatalf("FindChecksum = %q, %v; want %q, true", got, ok, sum)
	}
	// sha256sum -b writes a * before the name.
	if got, ok := FindChecksum(sum+" *work-linux-amd64\n", "work-linux-amd64"); !ok || got != sum {
		t.Errorf("binary-mode line: got %q, %v", got, ok)
	}
	if _, ok := FindChecksum(list, "work-darwin-arm64"); ok {
		t.Error("an asset that is not listed must not be found")
	}
	// A truncated digest is a corrupt list, not a checksum.
	if _, ok := FindChecksum("abc123  work-linux-amd64\n", "work-linux-amd64"); ok {
		t.Error("a short digest must be rejected")
	}
	if _, ok := FindChecksum("", "work-linux-amd64"); ok {
		t.Error("an empty list must not be found")
	}
}
