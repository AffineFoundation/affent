package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/memory"
	"github.com/affinefoundation/affent/internal/textutil"
)

const sessionMemoryBucketPreviewChars = 180
const maxSessionMemoryMutationBodyBytes = 32 * 1024

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
	Kinds      []string `json:"kinds,omitempty"`
	EntryCount int      `json:"entry_count"`
	CharsUsed  int      `json:"chars_used"`
	CharsLimit int      `json:"chars_limit,omitempty"`
	Percent    int      `json:"percent,omitempty"`
	NewestAt   string   `json:"newest_at,omitempty"`
	Preview    string   `json:"preview,omitempty"`
}

type sessionMemoryMutationRequest struct {
	Action     string `json:"action,omitempty"`
	Target     string `json:"target"`
	Kind       string `json:"kind,omitempty"`
	Topic      string `json:"topic,omitempty"`
	Content    string `json:"content,omitempty"`
	OldText    string `json:"old_text,omitempty"`
	NewContent string `json:"new_content,omitempty"`
}

func handleSessionMemory(pool *SessionPool, sessionID string, w http.ResponseWriter, r *http.Request) {
	if pool == nil {
		writeJSONError(w, http.StatusNotFound, "session not found", nil)
		return
	}
	if err := agent.ValidateSessionID(sessionID); err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid session id", err, "bad_request")
		return
	}
	if r.Method == http.MethodPost {
		handleSessionMemoryMutation(pool, sessionID, w, r)
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
	store, found, err := sessionMemoryStore(pool, sessionID)
	if err != nil || !found {
		return sessionMemoryResponse{}, found, err
	}
	resp, err := inspectSessionMemory(pool, sessionID, store)
	return resp, true, err
}

func handleSessionMemoryMutation(pool *SessionPool, sessionID string, w http.ResponseWriter, r *http.Request) {
	req, err := decodeSessionMemoryMutationRequest(w, r)
	if err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid memory request", err, "bad_request")
		return
	}
	store, found, err := sessionMemoryStore(pool, sessionID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "read session memory", err)
		return
	}
	if !found {
		writeJSONErrorTyped(w, http.StatusNotFound, "session not found", nil, "not_found")
		return
	}
	target := memory.MemoryTarget(strings.TrimSpace(req.Target))
	if target == "" {
		target = memory.TargetMemory
	}
	result, err := applySessionMemoryMutation(store, target, req)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "update memory", err)
		return
	}
	if !result.OK {
		writeJSONErrorTyped(w, http.StatusBadRequest, result.Message, nil, "memory_update_rejected")
		return
	}
	resp, err := inspectSessionMemory(pool, sessionID, store)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "read session memory", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}

func applySessionMemoryMutation(store *memory.FileMemoryStore, target memory.MemoryTarget, req sessionMemoryMutationRequest) (memory.MemoryResponse, error) {
	meta := memory.MemoryWriteMetadata{Kind: req.Kind}
	switch req.Action {
	case "", "add":
		return store.AddWithMetadata(target, req.Topic, req.Content, meta)
	case "remove":
		return store.Remove(target, req.Topic, req.OldText)
	case "replace":
		return store.ReplaceWithMetadata(target, req.Topic, req.OldText, req.NewContent, meta)
	default:
		return memory.MemoryResponse{Target: target, Topic: req.Topic, Message: "unsupported memory action"}, nil
	}
}

func sessionMemoryStore(pool *SessionPool, sessionID string) (*memory.FileMemoryStore, bool, error) {
	dir := pool.sessionDirPath(sessionID)
	if _, found, err := durableSessionDirInfo(dir); err != nil || !found {
		return nil, found, err
	}
	store := memory.NewFileMemoryStore("")
	store.MemoryDir = dir
	store.UserPath = pool.userMemoryPath(dir)
	return store, true, nil
}

func inspectSessionMemory(pool *SessionPool, sessionID string, store *memory.FileMemoryStore) (sessionMemoryResponse, error) {
	dir := pool.sessionDirPath(sessionID)
	userPath := pool.userMemoryPath(dir)

	resp := sessionMemoryResponse{
		SessionID:        sessionID,
		HasMemory:        durableMemoryExists(dir, userPath),
		SharedUserMemory: pool.cfg.SharedUserMemory,
		Topics:           []sessionMemoryBucket{},
	}
	userNewest := ""
	if userTopics, err := store.ListTopics(memory.TargetUser); err != nil {
		return sessionMemoryResponse{}, err
	} else if len(userTopics.Topics) > 0 {
		userNewest = userTopics.Topics[0].NewestAt
	}
	if bucket, ok, err := inspectSessionMemoryBucket(store, memory.TargetUser, "", userNewest); err != nil {
		return sessionMemoryResponse{}, err
	} else if ok || durableStatePathExists(userPath) {
		resp.User = &bucket
	}
	if bucket, ok, err := inspectSessionMemoryBucket(store, memory.TargetMemory, memory.CoreTopic, ""); err != nil {
		return sessionMemoryResponse{}, err
	} else if ok || durableStatePathExists(filepath.Join(dir, "core.md")) {
		resp.Core = &bucket
	}

	topics, err := store.ListTopics(memory.TargetMemory)
	if err != nil {
		return sessionMemoryResponse{}, err
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
			return sessionMemoryResponse{}, err
		}
		resp.Topics = append(resp.Topics, bucket)
	}
	return resp, nil
}

func decodeSessionMemoryMutationRequest(w http.ResponseWriter, r *http.Request) (sessionMemoryMutationRequest, error) {
	var req sessionMemoryMutationRequest
	if r.Body == nil || r.Body == http.NoBody {
		return req, errors.New("request body is required")
	}
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSessionMemoryMutationBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return req, err
	}
	var extra struct{}
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return req, errors.New("request body must contain a single JSON object")
	}
	req.Target = strings.TrimSpace(req.Target)
	req.Kind = strings.TrimSpace(req.Kind)
	req.Topic = strings.TrimSpace(req.Topic)
	req.Action = strings.TrimSpace(req.Action)
	req.Content = strings.TrimSpace(req.Content)
	req.OldText = strings.TrimSpace(req.OldText)
	req.NewContent = strings.TrimSpace(req.NewContent)
	switch req.Action {
	case "", "add":
		req.Action = "add"
		if req.Content == "" {
			return req, errors.New("content is required")
		}
		if req.Kind != "" && !memory.IsSupportedWriteKind(req.Kind) {
			return req, errors.New("unsupported memory kind")
		}
	case "remove":
		if req.OldText == "" {
			return req, errors.New("old_text is required")
		}
	case "replace":
		if req.Kind != "" && !memory.IsSupportedWriteKind(req.Kind) {
			return req, errors.New("unsupported memory kind")
		}
		if req.OldText == "" {
			return req, errors.New("old_text is required")
		}
		if req.NewContent == "" {
			return req, errors.New("new_content is required")
		}
	default:
		return req, errors.New("unsupported memory action")
	}
	if req.Content == "" && req.Action == "add" {
		return req, errors.New("content is required")
	}
	return req, nil
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
		Kinds:    append([]string(nil), out.Kinds...),
		NewestAt: newestAt,
		Preview:  sessionMemoryBucketPreview(out.Entries),
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

func sessionMemoryBucketPreview(entries []string) string {
	for i := len(entries) - 1; i >= 0; i-- {
		entry := textutil.CompactWhitespace(strings.TrimSpace(entries[i]))
		if entry != "" {
			return textutil.Preview(entry, sessionMemoryBucketPreviewChars)
		}
	}
	return ""
}
