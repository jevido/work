package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
)

// DefaultRepo is the GitHub repository Work releases from, as owner/name. It
// is the module path's home: dev.jevido/work is a vanity import path, and this
// is where the code and the releases actually live.
const DefaultRepo = "jevido/work"

// DefaultAPIBaseURL is GitHub's API root. It is a constant rather than being
// spelled into the request so the tests can serve the reply themselves.
const DefaultAPIBaseURL = "https://api.github.com"

// maxAPIBytes bounds the release JSON. A release with a hundred assets is a
// few tens of KiB; anything past this is not a reply worth parsing, and the
// limit means a hostile or broken endpoint cannot make Work allocate for it.
const maxAPIBytes = 1 << 20 // 1 MiB

// ChecksumsAsset is the name of the release asset listing the SHA-256 of every
// binary in the release, in `sha256sum` format. The release workflow writes it;
// Apply refuses to install a binary that is not in it.
const ChecksumsAsset = "checksums.txt"

// Release is the part of a GitHub release Work uses. The API returns far more
// than this and decoding into a narrow struct rather than a map keeps both the
// allocation count and the guesswork down.
type Release struct {
	TagName    string  `json:"tag_name"`
	HTMLURL    string  `json:"html_url"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Assets     []Asset `json:"assets"`
}

// Asset is one file attached to a release.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

// Asset returns the named asset, or false if this release has no such file.
// Releases carry a handful of assets, so this is a scan rather than a map: the
// map would cost more to build than the scan costs to run.
func (r *Release) Asset(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// AssetName is the release asset for one platform, and the single point of
// agreement between this package and .github/workflows/release.yml. Change it
// in one place and the other stops matching, so it lives here rather than
// being spelled out at both ends.
func AssetName(goos, goarch string) string {
	name := fmt.Sprintf("work-%s-%s", goos, goarch)
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// runningAssetName is the asset this binary would replace itself with.
func runningAssetName() string {
	return AssetName(runtime.GOOS, runtime.GOARCH)
}

// errNotModified reports that GitHub answered 304 to a conditional request, so
// the cached release is still the latest one. It is not a failure and is not
// logged as one.
var errNotModified = errors.New("update: not modified")

// fetchLatest asks GitHub for the newest release of the repository.
//
// The request is conditional on the ETag of the previous answer: a 304 is a
// few hundred bytes, decodes to nothing, and -- the reason this matters -- is
// not charged against the API rate limit, which is 60 requests an hour for an
// unauthenticated client and shared by everyone behind the same address. A
// long poll interval and a conditional request together mean Work's update
// check is invisible in that budget.
func (c *Checker) fetchLatest(ctx context.Context, etag string) (*Release, string, error) {
	url := c.baseURL + "/repos/" + c.repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", fmt.Errorf("update: build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "work/"+c.version)
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("update: fetch latest release: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotModified:
		return nil, etag, errNotModified
	case http.StatusNotFound:
		// A public repository with no published release answers 404 here,
		// which is a normal state for a repository that has just gone public
		// and not an error worth retrying differently.
		return nil, "", fmt.Errorf("update: %s has no published release", c.repo)
	default:
		return nil, "", fmt.Errorf("update: fetch latest release: %s", resp.Status)
	}

	var rel Release
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxAPIBytes)).Decode(&rel); err != nil {
		return nil, "", fmt.Errorf("update: parse latest release: %w", err)
	}
	if strings.TrimSpace(rel.TagName) == "" {
		return nil, "", errors.New("update: latest release has no tag")
	}
	return &rel, resp.Header.Get("ETag"), nil
}
