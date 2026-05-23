package browser

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// CachedResponse is the on-disk record format. Body is stored
// separately (raw bytes) so we don't pay base64 overhead in JSON.
type CachedResponse struct {
	URL        string      `json:"url"`
	StatusCode int         `json:"status_code"`
	Headers    http.Header `json:"headers"`
	Body       []byte      `json:"-"`
	FetchedAt  time.Time   `json:"fetched_at"`
}

// maxCachedResponseBodyBytes and maxCachedResponseMetaBytes are hard
// safety caps, not tuning knobs. Browser snapshots and text
// extraction do not need replaying giant binary/script bodies, and
// reading oversized cache files can otherwise spike process memory.
const (
	maxCachedResponseBodyBytes = 8 * 1024 * 1024
	maxCachedResponseMetaBytes = 256 * 1024
)

// ResponseCache is the interceptor's pluggable cache backend. v1
// ships FileResponseCache; production deployments may wire a redis-
// backed implementation for cross-process sharing.
type ResponseCache interface {
	Get(ctx context.Context, url string) (entry *CachedResponse, ok bool, err error)
	Put(ctx context.Context, url string, entry *CachedResponse) error
}

// FileResponseCache stores responses on disk under <dir>/<hash>.bin
// (body) + <dir>/<hash>.meta.json (headers / status / timestamp).
// Safe for use by multiple parallel sessions in the same process —
// each Put writes atomically (tempfile + rename).
//
// TTL semantics: a Get past TTL behaves like a miss but does NOT
// remove the entry; a subsequent Put overwrites. Call Sweep()
// periodically (or via StartSweeper) to actually delete expired
// entries from disk so the cache doesn't grow without bound on a
// long-running server.
type FileResponseCache struct {
	dir string
	ttl time.Duration

	mu sync.Mutex

	// sweepStop closes when StartSweeper's goroutine should exit.
	// Nil when no sweeper is running. Guarded by mu.
	sweepStop chan struct{}
}

// NewFileResponseCache creates the cache directory if missing.
// TTL of 0 disables expiry (cache forever).
func NewFileResponseCache(dir string, ttl time.Duration) (*FileResponseCache, error) {
	if dir == "" {
		return nil, errors.New("cache dir is required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}
	return &FileResponseCache{dir: dir, ttl: ttl}, nil
}

func (c *FileResponseCache) key(url string) string {
	sum := sha256.Sum256([]byte(url))
	return fmt.Sprintf("%x", sum[:16])
}

func (c *FileResponseCache) paths(url string) (meta, body string) {
	k := c.key(url)
	return filepath.Join(c.dir, k+".meta.json"), filepath.Join(c.dir, k+".bin")
}

// Get returns the cached entry for the URL, or ok=false on a miss.
// Errors reflect disk-level problems (corrupt JSON, permission
// failures); the caller should fall through to network.
func (c *FileResponseCache) Get(ctx context.Context, url string) (*CachedResponse, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	metaPath, bodyPath := c.paths(url)
	if info, err := os.Stat(metaPath); err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("stat meta: %w", err)
	} else if info.Size() > maxCachedResponseMetaBytes {
		return nil, false, nil
	}
	mf, err := os.Open(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("open meta: %w", err)
	}
	defer mf.Close()
	var meta CachedResponse
	if err := json.NewDecoder(mf).Decode(&meta); err != nil {
		return nil, false, fmt.Errorf("decode meta: %w", err)
	}
	if c.ttl > 0 && time.Since(meta.FetchedAt) > c.ttl {
		return nil, false, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if info, err := os.Stat(bodyPath); err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("stat body: %w", err)
	} else if info.Size() > maxCachedResponseBodyBytes {
		return nil, false, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	body, err := os.ReadFile(bodyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read body: %w", err)
	}
	meta.Body = body
	return &meta, true, nil
}

// Put writes a new entry. Each file is written via atomicWriteFile
// (tempfile + fsync + rename) so a crash mid-write can't surface as
// a half-written entry. We write the body first, then the meta — a
// concurrent Get that interleaves between the two will read old meta
// alongside the new body. The old meta's FetchedAt is still valid
// (it's the previous fetch's timestamp), so the TTL gate stays
// honest and the worst observable case is one stale-headers read.
// Inverting the order would just trade that for "new meta + old
// body", same shape of mismatch.
//
// We reject several classes of responses to avoid poisoning the cache
// for downstream replays:
//   - 4xx/5xx: typically transient (rate limit) or CF challenge body
//     served at 200 only on the actual challenge page; we still drop
//     anything non-2xx defensively.
//   - Cloudflare challenge platform URLs (`cdn-cgi/challenge-platform`,
//     `cdn-cgi/rum`, `cdn-cgi/zaraz`): each response embeds a
//     per-session ray / token; replaying it across sessions corrupts
//     the challenge state and propagates challenges to fresh sessions.
//   - HTML bodies that contain a Cloudflare challenge fingerprint
//     ("Just a moment...", "Verifying you are human", "cf-please-wait",
//     "challenge-platform"). When CF flips a real page into a
//     challenge, our cache must not freeze that challenge state — the
//     same URL is supposed to serve real content once the challenge
//     clears.
func (c *FileResponseCache) Put(ctx context.Context, url string, entry *CachedResponse) error {
	_, err := c.PutResult(ctx, url, entry)
	return err
}

// PutResult is FileResponseCache's richer write path. It reports
// whether the response was actually persisted; false,nil means the
// response was intentionally skipped (non-2xx, challenge, or too large).
func (c *FileResponseCache) PutResult(ctx context.Context, url string, entry *CachedResponse) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if entry == nil {
		return false, errors.New("nil entry")
	}
	if entry.StatusCode <= 0 || entry.StatusCode >= 400 {
		return false, nil
	}
	if len(entry.Body) > maxCachedResponseBodyBytes {
		return false, nil
	}
	if isChallengePathURL(url) {
		return false, nil
	}
	if looksLikeChallengeBody(entry.Body) {
		return false, nil
	}
	entry.URL = url
	if entry.FetchedAt.IsZero() {
		entry.FetchedAt = time.Now().UTC()
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}

	metaPath, bodyPath := c.paths(url)
	if err := atomicWriteFile(bodyPath, entry.Body); err != nil {
		return false, fmt.Errorf("write body: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	metaBytes, err := json.Marshal(entry)
	if err != nil {
		return false, fmt.Errorf("marshal meta: %w", err)
	}
	if err := atomicWriteFile(metaPath, metaBytes); err != nil {
		return false, fmt.Errorf("write meta: %w", err)
	}
	return true, nil
}

// challengePathSubstrings lists URL substrings that always indicate
// per-session Cloudflare (or similar) sub-resource state. Each of
// these either runs the proof-of-work dance, carries a one-time ray
// token, or exposes per-visit telemetry. Caching them poisons future
// sessions on the same origin and triggers spurious challenges
// downstream.
//
// All `/cdn-cgi/*` paths fall here on principle — every endpoint
// under that prefix is operated by Cloudflare's bot-management
// layer and either depends on or feeds per-session state. The
// expanded list catches the ones empirically observed leaking into
// the cache during CoinGecko runs (challenge-platform, rum, zaraz,
// scripts, speculation, trace).
var challengePathSubstrings = []string{
	"/cdn-cgi/",
	// html-load.cc is the third-party CF challenge JS CDN. Both
	// `/feed/...` (challenge instance frames) and `/script/...`
	// (challenge bootstrap) carry per-session tokens.
	"html-load.cc/feed/",
	"html-load.cc/script/",
}

func isChallengePathURL(u string) bool {
	lower := strings.ToLower(u)
	for _, s := range challengePathSubstrings {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

// challengeBodySignals are case-sensitive substrings that appear in
// the HTML of a Cloudflare challenge page. We only scan the first
// 8KiB to keep this cheap; CF puts the markers near the top.
var challengeBodySignals = [][]byte{
	[]byte("Just a moment..."),
	[]byte("Verifying you are human"),
	[]byte("cf-please-wait"),
	[]byte("cf-mitigated"),
	[]byte("challenge-platform"),
	[]byte("__cf_chl_"),
	[]byte("Sorry, you have been blocked"),
}

func looksLikeChallengeBody(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	head := body
	if len(head) > 8192 {
		head = head[:8192]
	}
	for _, sig := range challengeBodySignals {
		if bytes.Contains(head, sig) {
			return true
		}
	}
	return false
}

// Sweep walks the cache directory and removes entries whose
// FetchedAt is older than the configured TTL. Returns the count of
// deleted entries and any error.
//
// Race-safety: the per-entry verify-and-remove section takes c.mu so
// a concurrent Put cannot have its just-written file deleted by a
// Sweep that decided "stale" against the old content. Listing the
// directory is done unlocked because the result is just a worklist —
// any entries created mid-list naturally pass the freshness check.
//
// A TTL of zero (or negative) makes Sweep a no-op since "never
// expire" entries by definition can't be stale.
func (c *FileResponseCache) Sweep(ctx context.Context) (int, error) {
	if c.ttl <= 0 {
		return 0, nil
	}
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return 0, fmt.Errorf("read cache dir: %w", err)
	}
	deleted := 0
	for _, e := range entries {
		if ctx.Err() != nil {
			break
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".meta.json") {
			continue
		}
		metaPath := filepath.Join(c.dir, name)
		if c.sweepOne(metaPath) {
			deleted++
		}
	}
	return deleted, nil
}

// sweepOne re-verifies staleness under c.mu and removes the pair if
// still stale. Returns true on deletion. The mtime fast-path remains
// outside the lock so an obviously-fresh entry costs zero contention.
func (c *FileResponseCache) sweepOne(metaPath string) bool {
	cutoff := time.Now().Add(-c.ttl)
	info, err := os.Stat(metaPath)
	if err != nil {
		return false
	}
	if info.ModTime().After(cutoff) {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// Re-stat under lock so a Put that won the race between the
	// outer mtime check and the lock acquisition doesn't get
	// retroactively deleted.
	info2, err := os.Stat(metaPath)
	if err != nil {
		return false
	}
	if info2.ModTime().After(cutoff) {
		return false
	}
	if info2.Size() > maxCachedResponseMetaBytes {
		// Oversized meta is either corrupt or hostile input. Drop the
		// pair so future sweeps and Gets don't repeatedly touch it.
		bodyPath := strings.TrimSuffix(metaPath, ".meta.json") + ".bin"
		_ = os.Remove(metaPath)
		_ = os.Remove(bodyPath)
		return true
	}
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return false
	}
	var meta CachedResponse
	if err := json.Unmarshal(data, &meta); err != nil {
		// Corrupt meta — drop it.
		_ = os.Remove(metaPath)
		return true
	}
	if meta.FetchedAt.After(cutoff) {
		return false
	}
	bodyPath := strings.TrimSuffix(metaPath, ".meta.json") + ".bin"
	_ = os.Remove(metaPath)
	_ = os.Remove(bodyPath)
	return true
}

// StartSweeper runs Sweep on a ticker until StopSweeper is called or
// the cache's TTL is zero. Idempotent — second call is a no-op while
// a sweeper is already running. The reporter callback, if non-nil,
// receives the deletion count for each sweep round (useful for log
// integration).
func (c *FileResponseCache) StartSweeper(interval time.Duration, reporter func(deleted int)) {
	if c.ttl <= 0 || interval <= 0 {
		return
	}
	c.mu.Lock()
	if c.sweepStop != nil {
		c.mu.Unlock()
		return
	}
	stop := make(chan struct{})
	c.sweepStop = stop
	c.mu.Unlock()

	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		// Sweep once upfront so a long-stopped server doesn't carry
		// arbitrarily old entries through restart.
		if cnt, _ := c.Sweep(context.Background()); reporter != nil && cnt > 0 {
			reporter(cnt)
		}
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				cnt, _ := c.Sweep(context.Background())
				if reporter != nil && cnt > 0 {
					reporter(cnt)
				}
			}
		}
	}()
}

// StopSweeper signals the sweeper goroutine to exit. Idempotent.
func (c *FileResponseCache) StopSweeper() {
	c.mu.Lock()
	stop := c.sweepStop
	c.sweepStop = nil
	c.mu.Unlock()
	if stop != nil {
		close(stop)
	}
}

func atomicWriteFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	renamed := false
	defer func() {
		// Only Remove the tmp if the rename below didn't claim it. After
		// a successful rename the path is gone, and the unconditional
		// Remove would only spend a syscall returning ENOENT.
		if !renamed {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	// Sync before rename so the rename's atomicity carries through to
	// durable bytes on disk; without it, a crash between the write and
	// the kernel's writeback can leave the renamed file with truncated
	// or empty contents — the Get path then either Decode-fails or
	// returns a zero-status response that the LLM has to work around.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	renamed = true
	return nil
}
