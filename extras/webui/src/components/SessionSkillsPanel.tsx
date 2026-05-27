import { useMemo, useState, type FormEvent } from "react";
import type { SessionSkillInfo, SessionSkillInstallRequest } from "../api/sessions";
import { formatByteCount } from "../view/byteFormat";
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
  onReadSkill,
  onInstallSkill,
}: {
  skills?: readonly SessionSkillInfo[];
  loading?: boolean;
  error?: string;
  defaultOpen?: boolean;
  installEnabled?: boolean;
  onReadSkill?: (name: string) => Promise<SessionSkillInfo>;
  onInstallSkill?: (request: SessionSkillInstallRequest) => Promise<SessionSkillInfo>;
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
  const filteredSkills = useMemo(() => {
    const search = query.trim().toLowerCase();
    if (!search) return allSkills;
    return allSkills.filter((skill) =>
      [skill.name, skill.description, skill.source, skill.body_preview, ...(skill.triggers ?? []), ...(skill.required_tools ?? [])]
        .filter(Boolean)
        .join(" ")
        .toLowerCase()
        .includes(search),
    );
  }, [allSkills, query]);
  const runtimeCount = allSkills.filter((skill) => skill.runtime).length;
  const summary = loading ? "Loading skills" : error ? "Skills unavailable" : `${allSkills.length} ${allSkills.length === 1 ? "skill" : "skills"}`;
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
          </div>
        ) : null}
        {!loading && !error ? (
          <>
            {hasSearch || canInstall ? (
              <div className="session-skills-controls">
                {hasSearch ? (
                  <label className="session-skills-search">
                    <span>Search skills</span>
                    <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Search title or summary" />
                  </label>
                ) : null}
                {canInstall ? (
                  <button type="button" className="session-skills-add-toggle" onClick={() => setShowForm((open) => !open)}>
                    {showForm ? "Cancel" : "Add skill"}
                  </button>
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
                  return (
                    <details
                      key={skill.name}
                      className="session-skill-item"
                      onToggle={(event) => {
                        if (event.currentTarget.open) void loadBody(skill.name);
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
                        <div className="session-skill-meta">
                          {skill.source ? <span>Source: {skill.source}</span> : null}
                          <span>{formatByteCount(skill.body_bytes)}</span>
                          {activationSummary(skill) ? <span>{activationSummary(skill)}</span> : null}
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
                <div className="session-skills-empty">{allSkills.length > 0 ? "No matching skills." : "No skills listed."}</div>
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

function activationSummary(skill: SessionSkillInfo): string {
  const triggers = skill.triggers ?? skill.auto_activation?.any ?? [];
  if (triggers.length > 0) return `Triggers: ${triggers.slice(0, 3).join(", ")}${triggers.length > 3 ? "..." : ""}`;
  if (skill.required_tools && skill.required_tools.length > 0) return `Needs: ${skill.required_tools.join(", ")}`;
  return "";
}

function skillKindLabel(skill: SessionSkillInfo): string {
  return skill.runtime ? "Custom" : "Built in";
}

function activationCoverage(skills: readonly SessionSkillInfo[]): string {
  const triggerable = skills.filter((skill) => (skill.triggers?.length ?? skill.auto_activation?.any?.length ?? 0) > 0).length;
  const toolBound = skills.filter((skill) => (skill.required_tools?.length ?? 0) > 0).length;
  const parts: string[] = [];
  if (triggerable > 0) parts.push(`${triggerable} triggerable`);
  if (toolBound > 0) parts.push(`${toolBound} tool-bound`);
  return parts.length > 0 ? ` · ${parts.join(" · ")}` : "";
}

function skillSummaryTags(skill: SessionSkillInfo): string[] {
  const tags = [skillKindLabel(skill)];
  const triggers = skill.triggers ?? skill.auto_activation?.any ?? [];
  if (triggers.length > 0) tags.push(`${triggers.length} trigger${triggers.length === 1 ? "" : "s"}`);
  const requiredTools = skill.required_tools?.length ?? 0;
  if (requiredTools > 0) tags.push(`${requiredTools} tool${requiredTools === 1 ? "" : "s"}`);
  return tags;
}

function formatPanelError(err: unknown): string {
  if (err instanceof Error) return err.message;
  return "Unknown error";
}
