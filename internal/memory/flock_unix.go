//go:build unix

package memory

import (
	"os"
	"syscall"
)

// lockFile takes an exclusive cross-process file lock backed by flock(2)
// on a side-file (path + ".lock"). Returns a release function the caller
// must invoke; flock auto-releases on process exit so a crashed holder
// never deadlocks survivors.
//
// Held alongside the in-process mutex, this lets multiple affent
// processes serialize their read-modify-write cycles against the same
// MEMORY.md / USER.md.
func lockFile(path string) (func(), error) {
	lockPath := path + ".lock"
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return func() {}, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return func() {}, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
