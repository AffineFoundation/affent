package main

import (
	"encoding/json"
	"net/http"
	"sort"
	"time"
)

// statsResponse summarizes server + per-session activity at one
// snapshot in time. Useful for operators running large benchmark
// passes who want a quick "is the browser cache actually helping?"
// signal without standing up Prometheus.
type statsResponse struct {
	Listen          string                 `json:"listen"`
	Model           string                 `json:"model"`
	MaxSessions     int                    `json:"max_sessions"`
	ActiveSessions  int                    `json:"active_sessions"`
	ShuttingDown    bool                   `json:"shutting_down"`
	BrowserCacheDir string                 `json:"browser_cache_dir,omitempty"`
	ServerTime      string                 `json:"server_time"`
	Sessions        []sessionStatsResponse `json:"sessions"`
	Aggregate       aggregateStats         `json:"aggregate"`
}

type sessionStatsResponse struct {
	ID         string               `json:"id"`
	CreatedAt  string               `json:"created_at"`
	LastUsedAt string               `json:"last_used_at"`
	Usage      UsageSnapshot        `json:"usage"`
	Browser    BrowserStatsSnapshot `json:"browser"`
}

type aggregateStats struct {
	BlockedByType   int64 `json:"blocked_by_type"`
	BlockedByDomain int64 `json:"blocked_by_domain"`
	CacheHit        int64 `json:"cache_hit"`
	CacheMiss       int64 `json:"cache_miss"`
	NetworkFetch    int64 `json:"network_fetch"`
	InputTokens     int64 `json:"input_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	Turns           int64 `json:"turns"`
}

func handleStats(cfg Config, pool *SessionPool) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		// Snapshot the session pointers under the pool lock, then
		// release it before collecting per-session stats. Browser
		// counter reads are atomic but iterating them under pool.mu
		// would block GetOrCreate / GC / Shutdown for no reason.
		pool.mu.Lock()
		snap := make([]*Session, 0, len(pool.sessions))
		for _, s := range pool.sessions {
			snap = append(snap, s)
		}
		pool.mu.Unlock()

		sort.Slice(snap, func(i, j int) bool { return snap[i].ID < snap[j].ID })

		sess := make([]sessionStatsResponse, 0, len(snap))
		var agg aggregateStats
		for _, s := range snap {
			s.mu.Lock()
			created, lastUsed := s.createdAt, s.lastUsed
			s.mu.Unlock()
			b := s.BrowserStatsSnapshot()
			u := s.UsageSnapshot()
			sess = append(sess, sessionStatsResponse{
				ID:         s.ID,
				CreatedAt:  created.UTC().Format(time.RFC3339),
				LastUsedAt: lastUsed.UTC().Format(time.RFC3339),
				Usage:      u,
				Browser:    b,
			})
			agg.BlockedByType += b.BlockedByType
			agg.BlockedByDomain += b.BlockedByDomain
			agg.CacheHit += b.CacheHit
			agg.CacheMiss += b.CacheMiss
			agg.NetworkFetch += b.NetworkFetch
			agg.InputTokens += u.InputTokens
			agg.OutputTokens += u.OutputTokens
			agg.Turns += u.Turns
		}

		resp := statsResponse{
			Listen:          cfg.Listen,
			Model:           cfg.Model,
			MaxSessions:     cfg.MaxSessions,
			ActiveSessions:  len(sess),
			ShuttingDown:    pool.IsShuttingDown(),
			BrowserCacheDir: cfg.BrowserCacheDir,
			ServerTime:      time.Now().UTC().Format(time.RFC3339),
			Sessions:        sess,
			Aggregate:       agg,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}
