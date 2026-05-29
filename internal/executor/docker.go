package executor

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/affinefoundation/affent/internal/textutil"
)

// DockerExecExecutor runs the agent's shell + file tools against a
// pre-existing Docker container by shelling out to `docker exec`. Unlike
// LocalExecutor it offers real container isolation, and unlike the
// gateway module's per-session container it does NOT manage the
// container's lifecycle — the caller starts and stops it.
//
// Used by external eval harnesses (e.g. terminel) that already have a
// task container ready and just want affent to drive it.
//
// Implements both Executor and FileOps so the builtin file tools can
// transparently route through docker instead of touching the host fs.
type DockerExecExecutor struct {
	id          string
	containerID string

	// DockerBinary overrides the `docker` lookup. Defaults to "docker".
	DockerBinary string

	// DefaultCwd is the container path used when ExecOptions.WorkingDir
	// is empty. Empty here means "let docker pick" (typically /).
	DefaultCwd string
}

// NewDockerExecExecutor returns an Executor that runs commands inside
// the given container id via `docker exec`. sessionID is logging only.
func NewDockerExecExecutor(sessionID, containerID string) *DockerExecExecutor {
	return &DockerExecExecutor{id: sessionID, containerID: containerID}
}

// WithDefaultCwd returns a copy with DefaultCwd set. Chainable for ctor sites.
func (d *DockerExecExecutor) WithDefaultCwd(cwd string) *DockerExecExecutor {
	d.DefaultCwd = cwd
	return d
}

func (d *DockerExecExecutor) SessionID() string { return d.id }

// ContainerID is what we exec into. Exposed for adapters that want to
// log the target.
func (d *DockerExecExecutor) ContainerID() string { return d.containerID }

func (d *DockerExecExecutor) dockerBin() string {
	if d.DockerBinary != "" {
		return d.DockerBinary
	}
	return "docker"
}

// Exec runs `docker exec [-w cwd] [-e K=V ...] <cid> <cmd...>`.
func (d *DockerExecExecutor) Exec(ctx context.Context, cmd []string, opts ExecOptions) (ExecResult, error) {
	if len(cmd) == 0 {
		return ExecResult{}, errors.New("empty command")
	}
	if d.containerID == "" {
		return ExecResult{}, errors.New("docker exec executor: empty container id")
	}
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	args := []string{"exec"}
	if opts.Stdin != nil {
		args = append(args, "-i")
	}
	cwd, err := resolveWorkingDir(d.DefaultCwd, opts.WorkingDir)
	if err != nil {
		return ExecResult{ExitCode: -1}, err
	}
	if cwd != "" {
		args = append(args, "-w", cwd)
	}
	for _, e := range opts.Env {
		args = append(args, "-e", e)
	}
	args = append(args, d.containerID)
	args = append(args, cmd...)

	c := exec.CommandContext(ctx, d.dockerBin(), args...)
	if opts.Stdin != nil {
		c.Stdin = opts.Stdin
	}
	stdout := newCappingWriter(opts.MaxOutputBytes)
	stderr := newCappingWriter(opts.MaxOutputBytes)
	c.Stdout = stdout
	c.Stderr = stderr

	err = c.Run()
	exitCode := 0
	var execErr error
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exitCode = ee.ExitCode()
		} else {
			// Spawn failure / docker daemon unreachable / fork failure.
			execErr = fmt.Errorf("docker exec: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
			exitCode = -1
		}
	}
	// Same shape as LocalExecutor: a ctx-fired kill produces an
	// *ExitError with exit code -1; promote that into a clear timeout
	// error so the LLM sees "killed for running too long" instead of
	// "exited with -1".
	if ctx.Err() != nil && execErr == nil {
		execErr = fmt.Errorf("docker exec: %w (stderr: %s)", ctx.Err(), strings.TrimSpace(stderr.String()))
		exitCode = -1
	}
	return ExecResult{
		ExitCode: exitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
	}, execErr
}

// --- FileOps implementation ---
//
// All file ops resolve `path` as an absolute path inside the container.
// We deliberately do NOT honor a workspace-root sandbox here: the eval
// harness sets up tasks anywhere on the container filesystem (templates
// use /root, /app, /tmp, ...), and the agent legitimately needs to read
// arbitrary paths. Container isolation is the security boundary.

// ErrNotFoundInContainer is returned by file ops when the target path
// does not exist in the container. Callers (the agent through
// dispatch) get a clean "no such file or directory" string instead of
// a raw `cat`/`head` exit code dump.
var ErrNotFoundInContainer = errors.New("no such file or directory in container")

const (
	defaultDockerReadFileBytes = 64 * 1024
	maxDockerReadFileBytes     = 4 * 1024 * 1024

	dockerFileOpStatusCap     = 64 * 1024
	dockerFileOpStatOutputCap = 1024

	dockerBinaryProbeBytes       = 8192
	defaultDockerListFileEntries = 200
)

var dockerFileOpTimeout = 30 * time.Second

// ReadFile cats the file out of the container, capped at maxBytes.
// Returns ErrNotFoundInContainer when the path is missing so callers
// can distinguish that from permission or other shell errors.
func (d *DockerExecExecutor) ReadFile(ctx context.Context, path string, maxBytes int) (string, error) {
	if path == "" {
		return "", errors.New("path is required")
	}
	if maxBytes <= 0 {
		maxBytes = defaultDockerReadFileBytes
	}
	if maxBytes > maxDockerReadFileBytes {
		maxBytes = maxDockerReadFileBytes
	}
	// Pre-check existence so a "no such file" never gets confused with
	// a `head` failure on a real file (permission, EIO, etc.). The
	// `test -e` form is portable across busybox / ash / dash / bash.
	if exists, err := d.pathExists(ctx, path); err != nil {
		return "", err
	} else if !exists {
		return "", fmt.Errorf("%w: %s", ErrNotFoundInContainer, path)
	}
	// Read maxBytes+1 so we can detect truncation without reading the whole file.
	cmd := []string{"sh", "-c", "head -c " + strconv.Itoa(maxBytes+1) + " " + shellQuote(path)}
	res, err := d.Exec(ctx, cmd, ExecOptions{
		MaxOutputBytes: maxBytes + 1,
		Timeout:        dockerFileOpTimeout,
	})
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("read %s: exit %d: %s", path, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	out := res.Stdout
	// Refuse binary files. Same NUL-in-first-8K heuristic the host
	// readFileTool uses — dumping non-text into the model context is
	// wasted tokens and the shell tool with file/xxd/base64 is the
	// right escape hatch.
	probe := out
	if len(probe) > dockerBinaryProbeBytes {
		probe = probe[:dockerBinaryProbeBytes]
	}
	if strings.IndexByte(probe, 0) >= 0 {
		return "", fmt.Errorf("%s appears to be binary (contains null bytes); use shell with file/xxd/base64 to inspect", path)
	}
	if len(out) > maxBytes {
		return textutil.Preview(out, maxBytes, fmt.Sprintf("\n... [truncated; %d-byte cap]", maxBytes)), nil
	}
	return out, nil
}

// pathExists runs `test -e` inside the container.
func (d *DockerExecExecutor) pathExists(ctx context.Context, path string) (bool, error) {
	res, err := d.Exec(ctx, []string{"sh", "-c", "test -e " + shellQuote(path)}, ExecOptions{
		Timeout:        dockerFileOpTimeout,
		MaxOutputBytes: dockerFileOpStatOutputCap,
	})
	if err != nil {
		return false, err
	}
	if res.ExitCode == 0 {
		return true, nil
	}
	if res.ExitCode == 1 {
		return false, nil
	}
	return false, fmt.Errorf("test -e %s: exit %d: %s", path, res.ExitCode, strings.TrimSpace(res.Stderr))
}

// WriteFile overwrites the file inside the container. Creates parent dirs.
// Content goes through stdin (base64-decoded inside the container) so
// arbitrary bytes — including NULs and newlines — round-trip cleanly
// without quoting hazards.
func (d *DockerExecExecutor) WriteFile(ctx context.Context, path, content string) error {
	if path == "" {
		return errors.New("path is required")
	}
	parent := filepath.Dir(path)
	// Single shell invocation: mkdir parent, then base64 -d into the target.
	script := fmt.Sprintf("mkdir -p %s && base64 -d > %s", shellQuote(parent), shellQuote(path))
	enc := base64.StdEncoding.EncodeToString([]byte(content))
	res, err := d.Exec(ctx, []string{"sh", "-c", script}, ExecOptions{
		Stdin:          strings.NewReader(enc),
		Timeout:        dockerFileOpTimeout,
		MaxOutputBytes: dockerFileOpStatusCap,
	})
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("write %s: exit %d: %s", path, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return nil
}

// EditFile finds-and-replaces inside the container. Implements the same
// semantics as the host-fs version: `old` must occur at least once, and
// must be unique unless replaceAll is true. Implemented by reading the
// file out, mutating in Go, and writing it back — this gives us byte-exact
// semantics matching the host-fs path, including with arbitrary content
// (no sed/awk escaping landmines).
func (d *DockerExecExecutor) EditFile(ctx context.Context, path, oldStr, newStr string, replaceAll bool) (int, error) {
	if path == "" || oldStr == "" {
		return 0, errors.New("path and old are required")
	}
	body, err := d.readFileFull(ctx, path)
	if err != nil {
		return 0, err
	}
	n := strings.Count(body, oldStr)
	if n == 0 {
		return 0, fmt.Errorf("%w in %s", ErrEditNoMatch, path)
	}
	if n > 1 && !replaceAll {
		return n, fmt.Errorf("%w: old string occurs %d times in %s; pass replace_all=true or include more context to make it unique", ErrEditAmbiguousMatch, n, path)
	}
	var updated string
	if replaceAll {
		updated = strings.ReplaceAll(body, oldStr, newStr)
	} else {
		updated = strings.Replace(body, oldStr, newStr, 1)
	}
	if err := d.WriteFile(ctx, path, updated); err != nil {
		return 0, err
	}
	return n, nil
}

// ListFiles enumerates a single directory inside the container.
// Returns one FileEntry per child, capped at maxEntries. Returns
// ErrNotFoundInContainer when the directory is missing.
func (d *DockerExecExecutor) ListFiles(ctx context.Context, path string, maxEntries int) ([]FileEntry, error) {
	if path == "" {
		path = "/"
	}
	if maxEntries <= 0 {
		maxEntries = defaultDockerListFileEntries
	}
	if exists, err := d.pathExists(ctx, path); err != nil {
		return nil, err
	} else if !exists {
		return nil, fmt.Errorf("%w: %s", ErrNotFoundInContainer, path)
	}
	// Portable POSIX-sh enumeration. `ls -A1` lists dotfiles (minus
	// . and ..). For size: GNU `stat -c %s` works on Linux containers;
	// BSD `stat -f %z` is tried as a fallback for the rare cases the
	// container's stat is the BSD flavour. NUL-separated rows +
	// tab-separated fields keep ordinary whitespace parseable. Works
	// against busybox sh + ash + bash + dash.
	script := fmt.Sprintf(
		`cd %s && i=0; ls -A1 | while IFS= read -r f; do `+
			`i=$((i+1)); [ "$i" -gt %d ] && break; `+
			`if [ -d "$f" ]; then kind=dir; else kind=file; fi; `+
			`size=$(stat -c %%s "$f" 2>/dev/null || stat -f %%z "$f" 2>/dev/null || echo 0); `+
			`printf '%%s\t%%s\t%%s\0' "$kind" "$size" "$f"; `+
			`done`,
		shellQuote(path),
		maxEntries,
	)
	res, err := d.Exec(ctx, []string{"sh", "-c", script}, ExecOptions{
		MaxOutputBytes: DefaultExecOutputCap,
		Timeout:        dockerFileOpTimeout,
	})
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("list %s: exit %d: %s", path, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	var entries []FileEntry
	for _, rec := range strings.Split(res.Stdout, "\x00") {
		if rec == "" {
			continue
		}
		parts := strings.SplitN(rec, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		sz, _ := strconv.ParseInt(parts[1], 10, 64)
		entries = append(entries, FileEntry{
			Name:  parts[2],
			Size:  sz,
			IsDir: parts[0] == "dir",
		})
		if len(entries) >= maxEntries {
			break
		}
	}
	return entries, nil
}

// maxDockerEditFileBytes caps DockerExecExecutor.EditFile's
// read-modify-write path. Keep this aligned with the agent built-in
// edit_file local cap: exact replacement requires materializing the
// whole file, so large files should be inspected in chunks or modified
// by a targeted shell command inside the container.
const maxDockerEditFileBytes = maxDockerReadFileBytes

func (d *DockerExecExecutor) readFileFull(ctx context.Context, path string) (string, error) {
	size, err := d.fileSize(ctx, path)
	if err != nil {
		return "", err
	}
	if size > maxDockerEditFileBytes {
		return "", fmt.Errorf("read %s: file is %d bytes; edit_file supports files up to %d bytes (inspect chunks with read_file or shell, then apply a targeted command)", path, size, maxDockerEditFileBytes)
	}
	res, err := d.Exec(ctx, []string{"sh", "-c", "cat " + shellQuote(path)}, ExecOptions{
		MaxOutputBytes: maxDockerEditFileBytes,
		Timeout:        dockerFileOpTimeout,
	})
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("read %s: exit %d: %s", path, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	// cappingWriter's String() appends a banner containing
	// truncationMarker when the input exceeded the cap. Refuse the read
	// in that case — returning the truncated body would let EditFile
	// silently rewrite the file with the truncated content + banner
	// text appended.
	if strings.Contains(res.Stdout, truncationMarker) {
		return "", fmt.Errorf("read %s: file exceeds %d-byte read-all cap; edit not possible in-memory (split the file or operate via shell)", path, maxDockerEditFileBytes)
	}
	return res.Stdout, nil
}

func (d *DockerExecExecutor) fileSize(ctx context.Context, path string) (int64, error) {
	script := fmt.Sprintf("stat -c %%s %s 2>/dev/null || stat -f %%z %s 2>/dev/null", shellQuote(path), shellQuote(path))
	res, err := d.Exec(ctx, []string{"sh", "-c", script}, ExecOptions{
		MaxOutputBytes: 1024,
		Timeout:        dockerFileOpTimeout,
	})
	if err != nil {
		return 0, err
	}
	if res.ExitCode != 0 {
		return 0, fmt.Errorf("stat %s: exit %d: %s", path, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	size, err := strconv.ParseInt(strings.TrimSpace(res.Stdout), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("stat %s: parse size %q: %w", path, strings.TrimSpace(res.Stdout), err)
	}
	return size, nil
}

// shellQuote single-quotes a string for safe insertion into a bash -lc
// command line. Bash single-quoted strings interpret nothing except
// single quotes themselves, which we escape via the '"'"' idiom.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// Compile-time interface checks.
var _ Executor = (*DockerExecExecutor)(nil)
var _ FileOps = (*DockerExecExecutor)(nil)
