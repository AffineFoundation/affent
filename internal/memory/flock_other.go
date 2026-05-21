//go:build !unix

package memory

// lockFile is a no-op on non-Unix platforms. Callers fall back to the
// in-process mutex only; cross-process locking is not available here.
// Windows callers running multiple affent processes against the same
// MEMORY.md / USER.md should serialize externally.
func lockFile(path string) (func(), error) {
	return func() {}, nil
}
