package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleAccountSkills_ListsAndReadsSkillBodies(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	pool.cfg.EnableBuiltins = true

	r := httptest.NewRequest(http.MethodGet, "/v1/skills", nil)
	w := httptest.NewRecorder()
	handleAccountSkills(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", got, w.Body.String())
	}
	var list sessionSkillsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v body=%s", err, w.Body.String())
	}
	if list.SessionID != "account" || list.Count != len(list.Skills) || list.Count == 0 {
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
	if !found.AutoActivates {
		t.Fatalf("catalog entry should expose built-in skill as auto-activating: %+v", found)
	}
	if found.Activation == "" || found.Activation == "none" {
		t.Fatalf("catalog entry should expose activation summary: %+v", found)
	}

	r = httptest.NewRequest(http.MethodGet, "/v1/skills/coding_repair_workflow", nil)
	w = httptest.NewRecorder()
	handleAccountSkillRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", got, w.Body.String())
	}
	var detail sessionSkillResponse
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v body=%s", err, w.Body.String())
	}
	if detail.SessionID != "account" || detail.Skill.Name != "coding_repair_workflow" || !strings.Contains(detail.Skill.Body, "AFFENT ACTIVE SKILL: coding_repair_workflow") {
		t.Fatalf("detail response lost body: %+v", detail.Skill)
	}
}

func TestHandleAccountSkills_InstalledSkillActivatesForActiveAndNewSessions(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	pool.cfg.EnableBuiltins = true
	active, err := pool.GetOrCreate("skills-active")
	if err != nil {
		t.Fatalf("GetOrCreate active: %v", err)
	}
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
	var detail sessionSkillResponse
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode install: %v body=%s", err, w.Body.String())
	}
	if detail.SessionID != "account" || detail.Skill.Name != "account_demo" || !detail.Skill.Runtime || !strings.Contains(detail.Skill.Body, "account workflow") {
		t.Fatalf("install response = %+v", detail.Skill)
	}
	if !detail.Skill.AutoActivates {
		t.Fatalf("install response should expose skill as auto-activating: %+v", detail.Skill)
	}
	if detail.Skill.Activation != "any(account demo)" {
		t.Fatalf("install response activation = %q, want any(account demo)", detail.Skill.Activation)
	}
	if active.skillRegistry == nil {
		t.Fatal("active session skill registry missing")
	}
	if got := active.skillRegistry.Provide("please use account demo"); !strings.Contains(got, "account workflow") {
		t.Fatalf("account skill should be active immediately, got %q", got)
	}

	sess, err := pool.GetOrCreate("skills-new")
	if err != nil {
		t.Fatalf("GetOrCreate new: %v", err)
	}
	if sess.skillRegistry == nil {
		t.Fatal("new session skill registry missing")
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
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode read: %v body=%s", err, w.Body.String())
	}
	if detail.Skill.Name != "account_demo" || !strings.Contains(detail.Skill.Body, "account workflow") {
		t.Fatalf("account read response = %+v", detail.Skill)
	}
	if !detail.Skill.AutoActivates {
		t.Fatalf("account read response should preserve auto activation state: %+v", detail.Skill)
	}
	if detail.Skill.Activation != "any(account demo)" {
		t.Fatalf("account read activation = %q, want any(account demo)", detail.Skill.Activation)
	}

	r = httptest.NewRequest(http.MethodDelete, "/v1/skills/account_demo", nil)
	w = httptest.NewRecorder()
	handleAccountSkillRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("delete status = %d, want 200; body=%s", got, w.Body.String())
	}
	var deleted sessionSkillDeleteResponse
	if err := json.Unmarshal(w.Body.Bytes(), &deleted); err != nil {
		t.Fatalf("decode delete: %v body=%s", err, w.Body.String())
	}
	if deleted.SessionID != "account" || deleted.Name != "account_demo" || !deleted.Deleted {
		t.Fatalf("delete response = %+v", deleted)
	}
	if got := active.skillRegistry.Provide("please use account demo"); strings.Contains(got, "account workflow") {
		t.Fatalf("deleted account skill should be removed from active sessions, got %q", got)
	}
	r = httptest.NewRequest(http.MethodGet, "/v1/skills/account_demo", nil)
	w = httptest.NewRecorder()
	handleAccountSkillRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusNotFound {
		t.Fatalf("read deleted status = %d, want 404; body=%s", got, w.Body.String())
	}
}

func TestHandleAccountSkills_ManualSkillDoesNotAutoActivate(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	active, err := pool.GetOrCreate("skills-manual-active")
	if err != nil {
		t.Fatalf("GetOrCreate active: %v", err)
	}
	body := map[string]any{
		"name":        "manual_demo",
		"description": "Manual workflow.",
		"body":        "AFFENT ACTIVE SKILL: manual_demo\nUse this manual workflow only when explicitly read.",
	}
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(http.MethodPost, "/v1/skills", bytes.NewReader(raw))
	w := httptest.NewRecorder()
	handleAccountSkills(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", got, w.Body.String())
	}
	var detail sessionSkillResponse
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode install: %v body=%s", err, w.Body.String())
	}
	if detail.SessionID != "account" || detail.Skill.Name != "manual_demo" || !detail.Skill.Runtime || detail.Skill.AutoActivates {
		t.Fatalf("manual install response = %+v, want runtime non-auto-activating skill", detail.Skill)
	}
	if detail.Skill.Activation != "none" {
		t.Fatalf("manual install activation = %q, want none", detail.Skill.Activation)
	}
	if got := active.skillRegistry.Provide("please use manual demo"); got != "" {
		t.Fatalf("manual skill should not auto-activate in active session, got %q", got)
	}

	r = httptest.NewRequest(http.MethodGet, "/v1/skills", nil)
	w = httptest.NewRecorder()
	handleAccountSkills(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", got, w.Body.String())
	}
	var list sessionSkillsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v body=%s", err, w.Body.String())
	}
	for _, skill := range list.Skills {
		if skill.Name == "manual_demo" {
			if skill.AutoActivates {
				t.Fatalf("manual list entry should not auto-activate: %+v", skill)
			}
			if skill.Activation != "none" {
				t.Fatalf("manual list activation = %q, want none", skill.Activation)
			}
			return
		}
	}
	t.Fatalf("manual skill missing from list: %+v", list.Skills)
}

func TestHandleAccountSkills_RejectsDeletingBuiltInSkill(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	pool.cfg.EnableBuiltins = true

	r := httptest.NewRequest(http.MethodDelete, "/v1/skills/coding_repair_workflow", nil)
	w := httptest.NewRecorder()
	handleAccountSkillRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusConflict {
		t.Fatalf("delete built-in status = %d, want 409; body=%s", got, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "built-in skills cannot be deleted") {
		t.Fatalf("delete built-in response = %s", w.Body.String())
	}
}

func TestHandleAccountSkills_DeleteRuntimeOverrideRestoresBuiltIn(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	pool.cfg.EnableBuiltins = true
	active, err := pool.GetOrCreate("skills-shadow")
	if err != nil {
		t.Fatalf("GetOrCreate active: %v", err)
	}
	body := map[string]any{
		"name":        "coding_repair_workflow",
		"description": "Runtime override.",
		"body":        "AFFENT ACTIVE SKILL: coding_repair_workflow\nUse the runtime override.",
		"triggers":    []string{"runtime override"},
	}
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(http.MethodPost, "/v1/skills", bytes.NewReader(raw))
	w := httptest.NewRecorder()
	handleAccountSkills(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusCreated {
		t.Fatalf("install override status = %d, want 201; body=%s", got, w.Body.String())
	}
	overridden, ok := active.skillRegistry.Lookup("coding_repair_workflow")
	if !ok || strings.HasPrefix(overridden.Source, "embed:") || !strings.Contains(overridden.Body, "runtime override") {
		t.Fatalf("active registry should use runtime override, got ok=%v skill=%+v", ok, overridden)
	}

	r = httptest.NewRequest(http.MethodDelete, "/v1/skills/coding_repair_workflow", nil)
	w = httptest.NewRecorder()
	handleAccountSkillRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("delete override status = %d, want 200; body=%s", got, w.Body.String())
	}
	restored, ok := active.skillRegistry.Lookup("coding_repair_workflow")
	if !ok || !strings.HasPrefix(restored.Source, "embed:") || strings.Contains(restored.Body, "runtime override") {
		t.Fatalf("active registry should restore built-in after runtime delete, got ok=%v skill=%+v", ok, restored)
	}
}
