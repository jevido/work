//go:build !unix

package update

import (
	"fmt"
	"os"
	"os/exec"
)

// restart starts the newly installed binary and ends this process.
//
// Windows has no execve, so the restart is a new process plus an exit. The
// exit is immediate and skips the app's own shutdown, which is the trade this
// platform forces: the alternative is asking the window to close and hoping
// something downstream calls back here.
func restart(exe string) error {
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("update: restart %s: %w", exe, err)
	}
	// Released rather than waited on: the new Work outlives this one.
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("update: release %s: %w", exe, err)
	}
	os.Exit(0)
	return nil
}
