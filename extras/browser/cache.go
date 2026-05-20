package browser

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
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
// remove the entry; a subsequent Put overwrites. Operators can prune
// the dir from the outside.
type FileResponseCache struct {
	dir string
	ttl time.Duration

	mu sync.Mutex
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
func (c *FileResponseCache) Put(_ context.Context, url string, entry *CachedResponse) error {
	if entry == nil {
		return errors.New("nil entry")
	}
	// Don't cache failed responses; their content is usually a
	// non-deterministic CF challenge or rate-limit page.
	if entry.StatusCode <= 0 || entry.StatusCode >= 400 {
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
