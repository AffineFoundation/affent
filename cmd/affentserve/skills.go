package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/textutil"
)

const maxSessionSkillCreateBodyBytes = 96 * 1024

type sessionSkillsResponse struct {
	SessionID      string      `json:"session_id"`
	Count          int         `json:"count"`
	InstallEnabled bool        `json:"install_enabled"`
	Skills         []skillInfo `json:"skills"`
}

type sessionSkillResponse struct {
	SessionID string    `json:"session_id"`
	Skill     skillInfo `json:"skill"`
}

type sessionSkillDeleteResponse struct {
	SessionID string `json:"session_id"`
	Name      string `json:"name"`
	Deleted   bool   `json:"deleted"`
}

type sessionSkillInstallRequest struct {
	Name          string   `json:"name"`
	Description   string   `json:"description,omitempty"`
	Body          string   `json:"body"`
	Source        string   `json:"source,omitempty"`
	Triggers      []string `json:"triggers,omitempty"`
	RequiredTools []string `json:"required_tools,omitempty"`
}

type skillInfo struct {
	Name           string                     `json:"name"`
	Description    string                     `json:"description,omitempty"`
	Source         string                     `json:"source,omitempty"`
	Runtime        bool                       `json:"runtime"`
	RequiredTools  []string                   `json:"required_tools,omitempty"`
	Triggers       []string                   `json:"triggers,omitempty"`
	AutoActivation *agent.SkillAutoActivation `json:"auto_activation,omitempty"`
	AutoActivates  bool                       `json:"auto_activates"`
	BodyPreview    string                     `json:"body_preview,omitempty"`
	BodyBytes      int                        `json:"body_bytes"`
	Body           string                     `json:"body,omitempty"`
}

func handleAccountSkills(pool *SessionPool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handleAccountSkillsList(pool, w)
		case http.MethodPost:
			handleAccountSkillInstall(pool, w, r)
		default:
			writeJSONErrorTyped(w, http.StatusMethodNotAllowed, "method not allowed", nil, "bad_request")
		}
	}
}

func handleAccountSkillRoutes(pool *SessionPool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/v1/skills/")
		switch r.Method {
		case http.MethodGet:
			handleAccountSkillRead(pool, name, w)
		case http.MethodDelete:
			handleAccountSkillDelete(pool, name, w)
		default:
			writeJSONErrorTyped(w, http.StatusMethodNotAllowed, "method not allowed", nil, "bad_request")
		}
	}
}

func handleAccountSkillsList(pool *SessionPool, w http.ResponseWriter) {
	reg, err := accountSkillRegistry(pool)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "read skills", err)
		return
	}
	infos := skillCatalogInfos(reg, false)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(sessionSkillsResponse{
		SessionID:      "account",
		Count:          len(infos),
		InstallEnabled: pool != nil && workflowToolsEnabled(pool.cfg),
		Skills:         infos,
	})
}

func handleAccountSkillRead(pool *SessionPool, rawName string, w http.ResponseWriter) {
	name, err := url.PathUnescape(rawName)
	if err != nil || strings.TrimSpace(name) == "" {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid skill name", err, "bad_request")
		return
	}
	reg, err := accountSkillRegistry(pool)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "read skills", err)
		return
	}
	skill, ok := reg.Lookup(name)
	if !ok {
		writeJSONErrorTyped(w, http.StatusNotFound, "skill not found", nil, "not_found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(sessionSkillResponse{
		SessionID: "account",
		Skill:     skillInfoFromSkill(skill, true),
	})
}

func handleAccountSkillDelete(pool *SessionPool, rawName string, w http.ResponseWriter) {
	if pool == nil || !workflowToolsEnabled(pool.cfg) {
		writeJSONErrorTyped(w, http.StatusConflict, "skill install is not configured", nil, "skill_install_unavailable")
		return
	}
	name, err := url.PathUnescape(rawName)
	if err != nil || strings.TrimSpace(name) == "" {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid skill name", err, "bad_request")
		return
	}
	reg, err := accountSkillRegistry(pool)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "read skills", err)
		return
	}
	skill, ok := reg.Lookup(name)
	if !ok {
		writeJSONErrorTyped(w, http.StatusNotFound, "skill not found", nil, "not_found")
		return
	}
	if strings.HasPrefix(skill.Source, "embed:") {
		writeJSONErrorTyped(w, http.StatusConflict, "built-in skills cannot be deleted", nil, "builtin_skill_readonly")
		return
	}
	if err := agent.DeleteRuntimeSkill(accountSkillDir(pool), name); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "delete skill", err)
		return
	}
	removeAccountSkillFromActiveSessions(pool, name)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(sessionSkillDeleteResponse{
		SessionID: "account",
		Name:      name,
		Deleted:   true,
	})
}

func handleAccountSkillInstall(pool *SessionPool, w http.ResponseWriter, r *http.Request) {
	if pool == nil || !workflowToolsEnabled(pool.cfg) {
		writeJSONErrorTyped(w, http.StatusConflict, "skill install is not configured", nil, "skill_install_unavailable")
		return
	}
	req, err := decodeSessionSkillInstallRequest(w, r)
	if err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid skill install request", err, "bad_request")
		return
	}
	installed, err := agent.InstallRuntimeSkill(accountSkillDir(pool), agent.Skill{
		Name:           req.Name,
		Description:    req.Description,
		Source:         req.Source,
		Body:           req.Body,
		AutoActivation: agent.SkillAutoActivation{Any: req.Triggers},
		RequiredTools:  req.RequiredTools,
	})
	if err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "install skill", err, "bad_request")
		return
	}
	upsertAccountSkillIntoActiveSessions(pool, installed)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(sessionSkillResponse{
		SessionID: "account",
		Skill:     skillInfoFromSkill(installed, true),
	})
}

func accountSkillRegistry(pool *SessionPool) (*agent.SkillRegistry, error) {
	if pool == nil {
		return agent.DefaultSkillRegistry(), nil
	}
	reg, err := agent.RuntimeSkillRegistry(accountSkillDir(pool))
	if err != nil {
		return nil, err
	}
	return reg, nil
}

func sessionRuntimeSkillRegistry(pool *SessionPool, sessionSkillDir string) (*agent.SkillRegistry, error) {
	reg, err := accountSkillRegistry(pool)
	if err != nil {
		return nil, err
	}
	sessionReg, err := agent.RuntimeSkillRegistry(sessionSkillDir)
	if err != nil {
		return nil, err
	}
	for _, entry := range sessionReg.Catalog() {
		skill, ok := sessionReg.Lookup(entry.Name)
		if !ok || strings.HasPrefix(skill.Source, "embed:") {
			continue
		}
		if err := reg.Upsert(skill); err != nil {
			return nil, err
		}
	}
	return reg, nil
}

func accountSkillDir(pool *SessionPool) string {
	if pool == nil {
		return ""
	}
	return filepath.Join(accountSettingsDir(pool), "account-skills")
}

func upsertAccountSkillIntoActiveSessions(pool *SessionPool, skill agent.Skill) {
	if pool == nil {
		return
	}
	pool.mu.Lock()
	active := make([]*Session, 0, len(pool.sessions))
	for _, sess := range pool.sessions {
		active = append(active, sess)
	}
	pool.mu.Unlock()
	for _, sess := range active {
		if sess.skillRegistry != nil {
			_ = sess.skillRegistry.Upsert(skill)
		}
	}
}

func removeAccountSkillFromActiveSessions(pool *SessionPool, name string) {
	if pool == nil {
		return
	}
	pool.mu.Lock()
	active := make([]*Session, 0, len(pool.sessions))
	for _, sess := range pool.sessions {
		active = append(active, sess)
	}
	pool.mu.Unlock()
	var fallback agent.Skill
	hasFallback := false
	if reg := agent.DefaultSkillRegistry(); reg != nil {
		fallback, hasFallback = reg.Lookup(name)
	}
	for _, sess := range active {
		if sess.skillRegistry == nil {
			continue
		}
		sess.skillRegistry.Remove(name)
		if hasFallback {
			_ = sess.skillRegistry.Upsert(fallback)
		}
	}
}

func decodeSessionSkillInstallRequest(w http.ResponseWriter, r *http.Request) (sessionSkillInstallRequest, error) {
	var req sessionSkillInstallRequest
	if r.Body == nil || r.Body == http.NoBody {
		return req, errors.New("request body is required")
	}
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSessionSkillCreateBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return req, err
	}
	var extra struct{}
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return req, errors.New("request body must contain a single JSON object")
	}
	return req, nil
}

func skillCatalogInfos(reg *agent.SkillRegistry, includeBody bool) []skillInfo {
	if reg == nil {
		return nil
	}
	catalog := reg.Catalog()
	out := make([]skillInfo, 0, len(catalog))
	for _, entry := range catalog {
		skill, ok := reg.Lookup(entry.Name)
		if !ok {
			continue
		}
		out = append(out, skillInfoFromSkill(skill, includeBody))
	}
	return out
}

func skillInfoFromSkill(skill agent.Skill, includeBody bool) skillInfo {
	body := strings.TrimSpace(skill.Body)
	info := skillInfo{
		Name:          skill.Name,
		Description:   skill.Description,
		Source:        skill.Source,
		Runtime:       !strings.HasPrefix(skill.Source, "embed:"),
		RequiredTools: append([]string(nil), skill.RequiredTools...),
		Triggers:      append([]string(nil), skill.Triggers...),
		AutoActivates: skill.HasActivationRules(),
		BodyPreview:   textutil.Preview(body, 320),
		BodyBytes:     len(body),
	}
	if len(skill.AutoActivation.Any) > 0 || len(skill.AutoActivation.AllAny) > 0 {
		auto := skill.AutoActivation
		info.AutoActivation = &auto
	}
	if includeBody {
		info.Body = body
	}
	return info
}
