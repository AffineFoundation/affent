package main

import (
	"encoding/json"
	"net/http"
	"path/filepath"

	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/memory"
)

type sessionMemoryResponse struct {
	SessionID        string                `json:"session_id"`
	HasMemory        bool                  `json:"has_memory"`
	SharedUserMemory bool                  `json:"shared_user_memory,omitempty"`
	User             *sessionMemoryBucket  `json:"user,omitempty"`
	Core             *sessionMemoryBucket  `json:"core,omitempty"`
	Topics           []sessionMemoryBucket `json:"topics,omitempty"`
}

type sessionMemoryBucket struct {
	Target     string   `json:"target"`
	Topic      string   `json:"topic,omitempty"`
	Entries    []string `json:"entries,omitempty"`
	EntryCount int      `json:"entry_count"`
	CharsUsed  int      `json:"chars_used"`
	CharsLimit int      `json:"chars_limit,omitempty"`
	Percent    int      `json:"percent,omitempty"`
	NewestAt   string   `json:"newest_at,omitempty"`
}

func handleSessionMemory(pool *SessionPool, sessionID string, w http.ResponseWriter, _ *http.Request) {
	if pool == nil {
		writeJSONError(w, http.StatusNotFound, "session not found", nil)
		return
	}
	if err := agent.ValidateSessionID(sessionID); err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid session id", err, "bad_request")
		return
	}
	resp, found, err := readSessionMemory(pool, sessionID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "read session memory", err)
		return
	}
	if !found {
		writeJSONErrorTyped(w, http.StatusNotFound, "session not found", nil, "not_found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func readSessionMemory(pool *SessionPool, sessionID string) (sessionMemoryResponse, bool, error) {
	dir := pool.sessionDirPath(sessionID)
	if _, found, err := durableSessionDirInfo(dir); err != nil || !found {
		return sessionMemoryResponse{}, found, err
	}
	store := memory.NewFileMemoryStore("")
	store.MemoryDir = dir
	userPath := pool.userMemoryPath(dir)
	store.UserPath = userPath

	resp := sessionMemoryResponse{
		SessionID:        sessionID,
		HasMemory:        durableMemoryExists(dir, userPath),
		SharedUserMemory: pool.cfg.SharedUserMemory,
		Topics:           []sessionMemoryBucket{},
	}
	userNewest := ""
	if userTopics, err := store.ListTopics(memory.TargetUser); err != nil {
		return sessionMemoryResponse{}, true, err
	} else if len(userTopics.Topics) > 0 {
		userNewest = userTopics.Topics[0].NewestAt
	}
	if bucket, ok, err := inspectSessionMemoryBucket(store, memory.TargetUser, "", userNewest); err != nil {
		return sessionMemoryResponse{}, true, err
	} else if ok || durableStatePathExists(userPath) {
		resp.User = &bucket
	}
	if bucket, ok, err := inspectSessionMemoryBucket(store, memory.TargetMemory, memory.CoreTopic, ""); err != nil {
		return sessionMemoryResponse{}, true, err
	} else if ok || durableStatePathExists(filepath.Join(dir, "core.md")) {
		resp.Core = &bucket
	}

	topics, err := store.ListTopics(memory.TargetMemory)
	if err != nil {
		return sessionMemoryResponse{}, true, err
	}
	for _, summary := range topics.Topics {
		if summary.Topic == memory.CoreTopic {
			if resp.Core != nil && resp.Core.NewestAt == "" {
				resp.Core.NewestAt = summary.NewestAt
			}
			continue
		}
		bucket, _, err := inspectSessionMemoryBucket(store, memory.TargetMemory, summary.Topic, summary.NewestAt)
		if err != nil {
			return sessionMemoryResponse{}, true, err
		}
		resp.Topics = append(resp.Topics, bucket)
	}
	return resp, true, nil
}

func inspectSessionMemoryBucket(store *memory.FileMemoryStore, target memory.MemoryTarget, topic, newestAt string) (sessionMemoryBucket, bool, error) {
	out, err := store.Inspect(target, topic)
	if err != nil {
		return sessionMemoryBucket{}, false, err
	}
	bucket := sessionMemoryBucket{
		Target:   string(target),
		Topic:    out.Topic,
		Entries:  append([]string(nil), out.Entries...),
		NewestAt: newestAt,
	}
	if target == memory.TargetUser {
		bucket.Topic = "user"
	}
	if out.Usage != nil {
		bucket.EntryCount = out.Usage.EntryCount
		bucket.CharsUsed = out.Usage.CharsUsed
		bucket.CharsLimit = out.Usage.CharsLimit
		bucket.Percent = out.Usage.Percent
	}
	return bucket, bucket.EntryCount > 0, nil
}
