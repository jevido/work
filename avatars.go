package main

// Agent.Avatar is a filesystem path, and a webview cannot open one of those --
// it can only fetch a URL. This is the whole of what closes that gap: one
// route on the asset server that hands out the bytes of the picture in an
// agent's folder, so the office can draw a face instead of a coloured blob.

import (
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"

	"dev.jevido/work/internal/agents"
)

// AvatarRoute is the path the office asks on. Everything after it is an agent
// id, so an agent's picture is at /avatars/anton and nowhere else.
const AvatarRoute = "/avatars/"

// avatarMiddleware serves agent avatars, passing everything else through to
// the embedded frontend.
//
// root is a function rather than a string because the config folder can be
// changed while the app is running: the avatar served has to be the one in the
// folder currently in use, not the one that was in use when the window opened.
//
// Nothing here is a route into the filesystem. agents.AvatarPath will only
// return the two avatar filenames, only from directly inside the configured
// agents folder, and only after resolving the path and checking it is still in
// there -- so neither an id off the URL nor a symlinked agent folder reaches
// anything else. Every refusal is the same plain 404, because an agent with no
// avatar is the ordinary case rather than a failure, and the office draws its
// colour-blob fallback either way.
func avatarMiddleware(root func() string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, ok := strings.CutPrefix(r.URL.Path, AvatarRoute)
			if !ok {
				next.ServeHTTP(w, r)
				return
			}
			serveAvatar(w, r, root(), id)
		})
	}
}

// serveAvatar writes one agent's avatar, or a 404.
func serveAvatar(w http.ResponseWriter, r *http.Request, root, id string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	path, err := agents.AvatarPath(root, id)
	if err != nil {
		// Worth a line each: an id that does not name an agent means
		// something asked for a file it has no business with, and an
		// unresolvable path is a folder that moved under us. Both end up as a
		// blank desk on screen, which on its own explains nothing.
		log.Printf("avatars: %v", err)
		http.NotFound(w, r)
		return
	}
	if path == "" {
		http.NotFound(w, r)
		return
	}

	f, err := os.Open(path)
	if err != nil {
		// A file that was there during the scan and is gone now is somebody
		// deleting an avatar, not a problem to report.
		if !errors.Is(err, fs.ErrNotExist) {
			log.Printf("avatars: %v", err)
		}
		http.NotFound(w, r)
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}

	// The file belongs to the user and is edited outside Work, so it can be
	// replaced between two reloads without its name changing. no-cache keeps
	// the webview asking rather than showing yesterday's picture; ServeContent
	// answers an unchanged file with a 304 from the modification time, so the
	// usual case still costs no bytes. The content type comes from the
	// extension, which is why only .webp and .png are ever served.
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}
