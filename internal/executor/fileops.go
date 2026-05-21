package executor

import "context"

// FileOps is an optional extension implemented by Executors that can
// expose file-system primitives backed by the same isolation boundary
// the shell tool uses. The affent builtins detect this interface on
// `deps.Executor` and, when present, route read_file / write_file /
// edit_file / list_files through it instead of touching the host
// workspace directly.
//
// Implementing this is opt-in: LocalExecutor deliberately does NOT
// implement FileOps so the host-fs path keeps its semantics for the
// CLI / training-rig use case. DockerExecExecutor implements it because
// in container-mode the host fs is the wrong target — the agent should
// see and edit the container's view, not the harness's.
type FileOps interface {
	// ReadFile returns up to maxBytes of the file's contents.
	// If the file is larger, the returned string has a `... [truncated]`
	// suffix. maxBytes <= 0 means "use a sensible default".
	ReadFile(ctx context.Context, path string, maxBytes int) (string, error)

	// WriteFile overwrites the file with content. Parent directories
	// are created as needed.
	WriteFile(ctx context.Context, path, content string) error

	// EditFile does a find-and-replace within the file. `old` must
	// match exactly. Without replaceAll, `old` must occur exactly
	// once. Returns the number of occurrences replaced (>= 1 on
	// success).
	EditFile(ctx context.Context, path, old, new string, replaceAll bool) (int, error)

	// ListFiles enumerates the immediate children of `path`. Capped at
	// maxEntries.
	ListFiles(ctx context.Context, path string, maxEntries int) ([]FileEntry, error)
}

// FileEntry is one row in a ListFiles result.
type FileEntry struct {
	Name  string
	Size  int64
	IsDir bool
}
