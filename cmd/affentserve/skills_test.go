package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleSessionSkills_ListsAndReadsSkillBodies(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	pool.cfg.EnableBuiltins = true
	if _, err := pool.GetOrCreate("skills-active"); err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/skills-active/skills", nil)
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", got, w.Body.String())
	}
	var list sessionSkillsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v body=%s", err, w.Body.String())
	}
	if list.SessionID != "skills-active" || list.Count != len(list.Skills) || list.Count == 0 {
		t.Fatalf("list response = %+v", list)
	}
	if !list.InstallEnabled {
		t.Fatalf("install_enabled = false, want true")
	}
	var found skillInfo
	for _, skill := range list.Skills {
		if skill.Name == "coding_repair_workflow" {
			found = skill
			break
		}
	}
	if found.Name == "" || found.Description == "" || found.BodyPreview == "" || found.Body != "" {
		t.Fatalf("catalog entry should expose metadata and preview only: %+v", found)
	}

	r = httptest.NewRequest(http.MethodGet, "/v1/sessions/skills-active/skills/coding_repair_workflow", nil)
	w = httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", got, w.Body.String())
	}
	var detail sessionSkillResponse
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v body=%s", err, w.Body.String())
	}
	if detail.Skill.Name != "coding_repair_workflow" || !strings.Contains(detail.Skill.Body, "AFFENT ACTIVE SKILL: coding_repair_workflow") {
		t.Fatalf("detail response lost body: %+v", detail.Skill)
	}
}

func TestHandleSessionSkills_InstallsUserProvidedSkill(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	pool.cfg.EnableBuiltins = true
	if _, err := pool.GetOrCreate("skills-install"); err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	body := map[string]any{
		"name":        "manual_demo",
		"description": "Manual workflow.",
		"body":        "AFFENT ACTIVE SKILL: manual_demo\nUse this manual workflow.",
		"triggers":    []string{"manual demo"},
	}
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/skills-install/skills", bytes.NewReader(raw))
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", got, w.Body.String())
	}
	var detail sessionSkillResponse
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode install: %v body=%s", err, w.Body.String())
	}
	if detail.Skill.Name != "manual_demo" || !detail.Skill.Runtime || !strings.Contains(detail.Skill.Body, "manual workflow") {
		t.Fatalf("install response = %+v", detail.Skill)
	}
	sess := activeSessionByID(pool, "skills-install")
	if sess == nil || sess.skillRegistry == nil {
		t.Fatal("active session skill registry missing")
	}
	if got := sess.skillRegistry.Provide("please use manual demo"); !strings.Contains(got, "manual workflow") {
		t.Fatalf("installed skill should be active immediately, got %q", got)
	}
}

func TestHandleAccountSkills_InstalledSkillLoadsIntoNewSessions(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	pool.cfg.EnableBuiltins = true
	body := map[string]any{
		"name":        "account_demo",
		"description": "Account workflow.",
		"body":        "AFFENT ACTIVE SKILL: account_demo\nUse this account workflow.",
		"triggers":    []string{"account demo"},
	}
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(http.MethodPost, "/v1/skills", bytes.NewReader(raw))
	w := httptest.NewRecorder()
	handleAccountSkills(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", got, w.Body.String())
	}

	sess, err := pool.GetOrCreate("skills-account")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	if sess.skillRegistry == nil {
		t.Fatal("session skill registry missing")
	}
	if got := sess.skillRegistry.Provide("please use account demo"); !strings.Contains(got, "account workflow") {
		t.Fatalf("account skill should be active for new sessions, got %q", got)
	}

	r = httptest.NewRequest(http.MethodGet, "/v1/skills/account_demo", nil)
	w = httptest.NewRecorder()
	handleAccountSkillRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("read status = %d, want 200; body=%s", got, w.Body.String())
	}
	var detail sessionSkillResponse
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode read: %v body=%s", err, w.Body.String())
	}
	if detail.Skill.Name != "account_demo" || !strings.Contains(detail.Skill.Body, "account workflow") {
		t.Fatalf("account read response = %+v", detail.Skill)
	}
}
