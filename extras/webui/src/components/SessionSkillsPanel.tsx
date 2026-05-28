import { useMemo, useState, type FormEvent } from "react";
import type { SessionSkillInfo, SessionSkillInstallRequest } from "../api/sessions";
import type { UseAsDraft } from "../view/draftSource";
import {
  activationCoverage,
  activationSummary,
  skillDraft,
  skillEvidenceText,
  skillKindLabel,
  skillMatchesQuery,
  skillSearchMatches,
  skillSizeLabel,
  skillSummaryTags,
  skillUpdateDraft,
} from "../view/sessionSkills";
import { CopyButton } from "./CopyButton";
import { panelErrorSummary } from "./panelErrorSummary";

interface SkillBodyState {
  loading?: boolean;
  error?: string;
  body?: string;
}

export function SessionSkillsPanel({
  skills,
  loading = false,
  error,
  defaultOpen = false,
  installEnabled = false,
  onRefresh,
  onReadSkill,
  onInstallSkill,
  onUseAsDraft,
}: {
  skills?: readonly SessionSkillInfo[];
  loading?: boolean;
  error?: string;
  defaultOpen?: boolean;
  installEnabled?: boolean;
  onRefresh?: () => Promise<void> | void;
  onReadSkill?: (name: string) => Promise<SessionSkillInfo>;
  onInstallSkill?: (request: SessionSkillInstallRequest) => Promise<SessionSkillInfo>;
  onUseAsDraft?: UseAsDraft;
}) {
  const [query, setQuery] = useState("");
  const [bodyByName, setBodyByName] = useState<Record<string, SkillBodyState>>({});
  const [panelOpen, setPanelOpen] = useState(defaultOpen);
  const [showForm, setShowForm] = useState(false);
  const [form, setForm] = useState({ name: "", description: "", triggers: "", requiredTools: "", body: "" });
  const [installError, setInstallError] = useState<string | undefined>();
  const [installing, setInstalling] = useState(false);
  const allSkills = skills ?? [];
  const hasSearch = allSkills.length > 0;
  const canInstall = installEnabled && !!onInstallSkill;
  const trimmedQuery = query.trim();
  const filteredSkills = useMemo(() => {
    if (!trimmedQuery) return allSkills;
    return allSkills.filter((skill) => skillMatchesQuery(skill, trimmedQuery));
  }, [allSkills, trimmedQuery]);
  const runtimeCount = allSkills.filter((skill) => skill.runtime).length;
  const summary = loading
    ? "Loading skills"
    : error
      ? "Skills unavailable"
      : allSkills.length === 0
        ? "No reusable workflows"
        : `${allSkills.length} ${allSkills.length === 1 ? "skill" : "skills"}`;
  const summaryDetail = loading
    ? "Fetching reusable workflows."
    : error
      ? panelErrorSummary("Skills API", error)
      : allSkills.length === 0
        ? "No reusable workflows listed."
      : runtimeCount > 0
        ? `${runtimeCount} custom · ${allSkills.length - runtimeCount} built in${activationCoverage(allSkills)}`
        : `${allSkills.length} built in${activationCoverage(allSkills)}`;

  async function loadBody(name: string) {
    if (!onReadSkill || bodyByName[name]?.body || bodyByName[name]?.loading) return;
    setBodyByName((current) => ({ ...current, [name]: { ...current[name], loading: true, error: undefined } }));
    try {
      const skill = await onReadSkill(name);
      setBodyByName((current) => ({ ...current, [name]: { body: skill.body ?? "" } }));
    } catch (err) {
      setBodyByName((current) => ({ ...current, [name]: { error: formatPanelError(err) } }));
    }
  }

  async function submitSkill(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!onInstallSkill || installing) return;
    setInstallError(undefined);
    setInstalling(true);
    try {
      const installed = await onInstallSkill({
        name: form.name,
        description: form.description || undefined,
        body: form.body,
        triggers: splitList(form.triggers),
        required_tools: splitList(form.requiredTools),
      });
      setBodyByName((current) => ({ ...current, [installed.name]: { body: installed.body ?? form.body } }));
      setForm({ name: "", description: "", triggers: "", requiredTools: "", body: "" });
      setShowForm(false);
    } catch (err) {
      setInstallError(formatPanelError(err));
    } finally {
      setInstalling(false);
    }
  }

  return (
    <details
      className="session-skills-panel"
      data-testid="session-skills-panel"
      open={panelOpen}
      onToggle={(event) => setPanelOpen(event.currentTarget.open)}
    >
      <summary className="session-skills-summary">
        <span className="session-skills-kicker">Skills</span>
        <strong>{summary}</strong>
        <span>{summaryDetail}</span>
      </summary>
      <div className="session-skills-body">
        {loading ? <div className="session-skills-empty">Loading account skills...</div> : null}
        {!loading && error ? (
          <div className="session-skills-empty error" role="alert">
            {error}
            {onRefresh ? (
              <button type="button" className="ghost-action" onClick={() => void onRefresh()}>
                Retry
              </button>
            ) : null}
          </div>
        ) : null}
        {!loading && !error ? (
          <>
            {hasSearch || canInstall || onRefresh ? (
              <div className="session-skills-controls">
                {hasSearch ? (
                  <label className="session-skills-search">
                    <span>Search skills</span>
                    <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Search title or summary" />
                  </label>
                ) : null}
                {trimmedQuery ? (
                  <button type="button" className="ghost-action" onClick={() => setQuery("")}>
                    Clear
                  </button>
                ) : null}
                {canInstall ? (
                  <button type="button" className="session-skills-add-toggle" onClick={() => setShowForm((open) => !open)}>
                    {showForm ? "Cancel" : "Add skill"}
                  </button>
                ) : null}
                {onRefresh ? (
                  <button type="button" className="ghost-action" onClick={() => void onRefresh()}>
                    Refresh
                  </button>
                ) : null}
                {trimmedQuery ? (
                  <span className="session-search-count" data-testid="session-skills-search-count">
                    {filteredSkills.length} {filteredSkills.length === 1 ? "skill" : "skills"} matching "{trimmedQuery}"
                  </span>
                ) : null}
              </div>
            ) : null}
            {showForm ? (
              <form className="session-skill-form" onSubmit={submitSkill}>
                <label>
                  <span>Name</span>
                  <input value={form.name} onChange={(event) => setForm((current) => ({ ...current, name: event.target.value }))} placeholder="my_skill" required />
                </label>
                <label>
                  <span>Summary</span>
                  <input
                    value={form.description}
                    onChange={(event) => setForm((current) => ({ ...current, description: event.target.value }))}
                    placeholder="When this skill should be used"
                  />
                </label>
                <label>
                  <span>Triggers</span>
                  <input
                    value={form.triggers}
                    onChange={(event) => setForm((current) => ({ ...current, triggers: event.target.value }))}
                    placeholder="comma or newline separated"
                  />
                </label>
                <label>
                  <span>Required tools</span>
                  <input
                    value={form.requiredTools}
                    onChange={(event) => setForm((current) => ({ ...current, requiredTools: event.target.value }))}
                    placeholder="workspace, browser, web"
                  />
                </label>
                <label className="session-skill-form-body">
                  <span>Full content</span>
                  <textarea
                    value={form.body}
                    onChange={(event) => setForm((current) => ({ ...current, body: event.target.value }))}
                    placeholder="AFFENT ACTIVE SKILL: my_skill&#10;Use this workflow..."
                    required
                  />
                </label>
                {installError ? <div className="session-skills-empty error">{installError}</div> : null}
                <button type="submit" className="session-skills-add-submit" disabled={installing}>
                  {installing ? "Adding" : "Save skill"}
                </button>
              </form>
            ) : null}
            <div className="session-skills-list" data-testid="session-skills-list">
              {filteredSkills.length > 0 ? (
                filteredSkills.map((skill) => {
                  const bodyState = bodyByName[skill.name];
                  const body = bodyState?.body ?? skill.body;
                  const searchMatches = trimmedQuery ? skillSearchMatches(skill, trimmedQuery) : [];
                  return (
                    <details
                      key={skill.name}
                      className="session-skill-item"
                      open={trimmedQuery ? true : undefined}
                      onToggle={(event) => {
                        if (event.currentTarget.open && !trimmedQuery) void loadBody(skill.name);
                      }}
                    >
                      <summary>
                        <span className="session-skill-title">
                          <strong>{skill.name}</strong>
                          <span>{skillKindLabel(skill)}</span>
                        </span>
                        <span className="session-skill-desc">{skill.description || "No summary"}</span>
                        <span className="session-skill-status">
                          {skillSummaryTags(skill).map((tag) => (
                            <span key={tag} title={tag}>{tag}</span>
                          ))}
                        </span>
                      </summary>
                      <div className="session-skill-detail">
                        {searchMatches.length > 0 ? (
                          <div className="session-skill-matches" data-testid={`skill-search-matches-${skill.name}`}>
                            {searchMatches.map((match) => <span key={match}>{match}</span>)}
                          </div>
                        ) : null}
                        <div className="session-skill-meta">
                          {skill.source ? <span>Source: {skill.source}</span> : null}
                          <span>{skillSizeLabel(skill)}</span>
                          {activationSummary(skill) ? <span>{activationSummary(skill)}</span> : null}
                        </div>
                        <div className="session-skill-actions">
                          <CopyButton label="Copy skill evidence" value={skillEvidenceText(skill, body)} className="node-action" />
                          {onUseAsDraft ? (
                            <>
                              <button type="button" className="node-action" onClick={() => onUseAsDraft(skillDraft(skill, body), "skill")}>
                                Use skill as draft
                              </button>
                              <button type="button" className="node-action" onClick={() => onUseAsDraft(skillUpdateDraft(skill, body), "skill")}>
                                Update as draft
                              </button>
                            </>
                          ) : null}
                        </div>
                        {bodyState?.loading ? <div className="session-skills-empty">Loading full content...</div> : null}
                        {bodyState?.error ? <div className="session-skills-empty error">{bodyState.error}</div> : null}
                        {body ? (
                          <>
                            <CopyButton label="Copy" value={body} className="node-action" />
                            <pre className="session-skill-body">{body}</pre>
                          </>
                        ) : !bodyState?.loading && !bodyState?.error ? (
                          <p className="session-skill-preview">{skill.body_preview || "No content preview."}</p>
                        ) : null}
                      </div>
                    </details>
                  );
                })
              ) : (
                <div className="session-skills-empty">{allSkills.length > 0 ? "No matching skills." : emptySkillsText(canInstall)}</div>
              )}
            </div>
          </>
        ) : null}
      </div>
    </details>
  );
}

function splitList(text: string): string[] | undefined {
  const parts = text
    .split(/[,\n]/)
    .map((part) => part.trim())
    .filter(Boolean);
  return parts.length > 0 ? parts : undefined;
}

function emptySkillsText(canInstall: boolean): string {
  return canInstall ? "No reusable workflows saved yet." : "No skills returned by this runtime.";
}

function formatPanelError(err: unknown): string {
  if (err instanceof Error) return err.message;
  return "Unknown error";
}
