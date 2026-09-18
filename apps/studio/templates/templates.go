// Package templates holds the files Work writes into a config folder that does
// not have them yet: the starting team, and the template somebody copies to add
// to it.
//
// On disk rather than in string constants, and that is the point of the
// package. These are documents somebody will open, edit and argue with in their
// own agents folder, so they are written here the way they will be read there --
// one file per agent, reviewable as a diff, rather than as Go string literals
// nobody can see the shape of.
//
// Embedded, because the binary ships alone. A release is one file that somebody
// puts on their PATH; anything it needs to write on first run has to already be
// inside it.
package templates

import (
	"embed"
	"io/fs"
)

// all: so that _template comes along. The embed directive skips names starting
// with _ or . unless it is told not to, and _template is the one folder here
// whose name begins with an underscore -- deliberately, because that is also
// how Work's own scanner knows it is not an agent.
//
//go:embed all:agents
var files embed.FS

// Agents is the starting agents folder: one directory per agent, each with a
// PERSONALITY.md and optionally a skills/.
func Agents() fs.FS {
	sub, err := fs.Sub(files, "agents")
	if err != nil {
		// Unreachable: the directory is embedded above, so a failure here is a
		// build that should not have linked.
		panic("templates: embedded agents missing: " + err.Error())
	}
	return sub
}
