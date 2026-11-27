//go:build unix

package cmd

import (
	"os"
	"syscall"
)

// execBinary runs the binary again in this process, with the same arguments
// and environment. The process ID stays, so systemd sees no stop; after an
// upgrade it is the new binary. Go opens every file and socket close-on-exec,
// so nothing (the web interface's port included) stays open.
func execBinary() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return syscall.Exec(exe, os.Args, os.Environ())
}
