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
func (c *FileResponseCache) Get(_ context.Context, url string) (*CachedResponse, bool, error) {
	metaPath, bodyPath := c.paths(url)
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

// Put writes a new entry. Atomic: tempfile + rename for each of the
// two files. We accept the small (~ms) window where meta lands first;
// a concurrent Get reading just-the-meta then reading the still-old
// body is degenerate but not corrupting (the old body is well-formed).
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
func (c *FileResponseCache) Put(_ context.Context, url string, entry *CachedResponse) error {
	if entry == nil {
		return errors.New("nil entry")
	}
	if entry.StatusCode <= 0 || entry.StatusCode >= 400 {
		return nil
	}
	if isChallengePathURL(url) {
		return nil
	}
	if looksLikeChallengeBody(entry.Body) {
		return nil
	}
	entry.URL = url
	if entry.FetchedAt.IsZero() {
		entry.FetchedAt = time.Now().UTC()
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	metaPath, bodyPath := c.paths(url)
	if err := atomicWriteFile(bodyPath, entry.Body); err != nil {
		return fmt.Errorf("write body: %w", err)
	}
	metaBytes, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal meta: %w", err)
	}
	if err := atomicWriteFile(metaPath, metaBytes); err != nil {
		return fmt.Errorf("write meta: %w", err)
	}
	return nil
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
// Safe to call concurrently with Get/Put — file deletion is atomic
// from the filesystem's perspective, and we tolerate partial
// half-deleted pairs (meta gone, body remains) by treating a missing
// meta.json as a miss in Get.
//
// A TTL of zero (or negative) makes Sweep a no-op since "never
// expire" entries by definition can't be stale.
func (c *FileResponseCache) Sweep(ctx context.Context) (int, error) {
	if c.ttl <= 0 {
		return 0, nil
	}
	cutoff := time.Now().Add(-c.ttl)
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
		// Cheap pre-filter on filesystem mtime — if the file was
		// touched after cutoff we know it can't be older than TTL
		// without reading it. (mtime updates on rename, so freshly-
		// written entries always pass this check.)
		info, err := os.Stat(metaPath)
		if err != nil {
			continue
		}
		if info.ModTime().After(cutoff) {
			continue
		}
		// Confirm via on-disk content: someone may have backdated
		// the mtime, or the body's FetchedAt may differ from mtime.
		data, err := os.ReadFile(metaPath)
		if err != nil {
			continue
		}
		var meta CachedResponse
		if err := json.Unmarshal(data, &meta); err != nil {
			// Corrupt meta — drop it.
			_ = os.Remove(metaPath)
			deleted++
			continue
		}
		if meta.FetchedAt.After(cutoff) {
			continue
		}
		bodyPath := strings.TrimSuffix(metaPath, ".meta.json") + ".bin"
		_ = os.Remove(metaPath)
		_ = os.Remove(bodyPath)
		deleted++
	}
	return deleted, nil
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
	defer func() {
		// If we didn't rename successfully, clean the tmp file.
		_ = os.Remove(tmpName)
	}()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
