//go:build unix

package update

import (
	"fmt"
	"os"
	"syscall"
)

// restart replaces this process with the newly installed binary.
//
// execve rather than spawn-and-exit: the process keeps its PID, its parent,
// its terminal and its controlling session, so a Work started from a shell,
// a desktop launcher or a supervisor comes back as the same process rather
// than as an orphan its launcher has lost track of. There is also no window in
// which two Works are alive.
//
// It only returns if the exec failed. Nothing after it runs on success --
// including deferred functions -- which is why the swap is complete and
// verified before this is called.
func restart(exe string) error {
	argv := append([]string{exe}, os.Args[1:]...)
	if err := syscall.Exec(exe, argv, os.Environ()); err != nil {
		return fmt.Errorf("update: restart %s: %w", exe, err)
	}
	return nil
}
