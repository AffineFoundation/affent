import { useMemo, useState, type FormEvent } from "react";
import type { SessionSkillInfo, SessionSkillInstallRequest } from "../api/sessions";
import type { UseAsDraft } from "../view/draftSource";
import {
  activationCoverage,
  activationSummary,
  matchingSkillsForPrompt,
  skillDraft,
  skillEvidenceText,
  skillKindLabel,
  skillMatchesQuery,
  skillOriginLabel,
  skillSearchMatches,
  skillSizeLabel,
  skillSummaryTags,
  skillTriggers,
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
  surface = false,
  installEnabled = false,
  onRefresh,
  onReadSkill,
  onInstallSkill,
  onDeleteSkill,
  onUseAsDraft,
}: {
  skills?: readonly SessionSkillInfo[];
  loading?: boolean;
  error?: string;
  defaultOpen?: boolean;
  surface?: boolean;
  installEnabled?: boolean;
  onRefresh?: () => Promise<void> | void;
  onReadSkill?: (name: string) => Promise<SessionSkillInfo>;
  onInstallSkill?: (request: SessionSkillInstallRequest) => Promise<SessionSkillInfo>;
  onDeleteSkill?: (name: string) => Promise<void> | void;
  onUseAsDraft?: UseAsDraft;
}) {
  const [query, setQuery] = useState("");
  const [bodyByName, setBodyByName] = useState<Record<string, SkillBodyState>>({});
  const [panelOpen, setPanelOpen] = useState(defaultOpen);
  const [showForm, setShowForm] = useState(false);
  const [form, setForm] = useState({ name: "", description: "", triggers: "", requiredTools: "", body: "" });
  const [installError, setInstallError] = useState<string | undefined>();
  const [skillActionStatus, setSkillActionStatus] = useState<{ tone: "success" | "error"; message: string } | undefined>();
  const [installing, setInstalling] = useState(false);
  const [editingSkillName, setEditingSkillName] = useState<string | undefined>();
  const [deleteConfirmName, setDeleteConfirmName] = useState<string | undefined>();
  const [deletingSkillName, setDeletingSkillName] = useState<string | undefined>();
  const [selectedSkillName, setSelectedSkillName] = useState<string | undefined>();
  const [skillFilter, setSkillFilter] = useState<SkillFilter>("all");
  const [activationProbe, setActivationProbe] = useState("");
  const allSkills = skills ?? [];
  const hasSearch = allSkills.length > 0;
  const canInstall = installEnabled && !!onInstallSkill;
  const trimmedQuery = query.trim();
  const filteredSkills = useMemo(() => {
    return allSkills
      .filter((skill) => skillMatchesFilter(skill, skillFilter))
      .filter((skill) => !trimmedQuery || skillMatchesQuery(skill, trimmedQuery));
  }, [allSkills, skillFilter, trimmedQuery]);
  const activationMatches = useMemo(() => matchingSkillsForPrompt(allSkills, activationProbe), [activationProbe, allSkills]);
  const focusedSkill = useMemo(() => {
    if (filteredSkills.length === 0) return undefined;
    const selected = selectedSkillName ? filteredSkills.find((skill) => skill.name === selectedSkillName) : undefined;
    if (selected) return selected;
    return filteredSkills.find((skill) => skill.runtime)
      ?? filteredSkills.find((skill) => (skill.required_tools?.length ?? 0) > 0)
      ?? filteredSkills[0];
  }, [filteredSkills, selectedSkillName]);
  const runtimeCount = allSkills.filter((skill) => skill.runtime).length;
  const canDraftSkill = !!onUseAsDraft;
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
    if (installing) return;
    if (!onInstallSkill) {
      if (onUseAsDraft && form.name.trim() && form.body.trim()) {
        setSkillActionStatus(undefined);
        onUseAsDraft(manualSkillDraft(form), "skill");
        setForm({ name: "", description: "", triggers: "", requiredTools: "", body: "" });
        setShowForm(false);
        setSkillActionStatus({ tone: "success", message: `${form.name.trim()} draft prepared.` });
      }
      return;
    }
    setInstallError(undefined);
    setSkillActionStatus(undefined);
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
      setEditingSkillName(undefined);
      setShowForm(false);
      setSkillActionStatus({ tone: "success", message: `${installed.name} saved.` });
    } catch (err) {
      const message = formatPanelError(err);
      setInstallError(message);
      setSkillActionStatus({ tone: "error", message });
    } finally {
      setInstalling(false);
    }
  }

  async function editSkill(skill: SessionSkillInfo) {
    if (!skill.runtime) return;
    setInstallError(undefined);
    setSkillActionStatus(undefined);
    setDeleteConfirmName(undefined);
    let body = bodyByName[skill.name]?.body ?? skill.body;
    if (!body && onReadSkill) {
      setBodyByName((current) => ({ ...current, [skill.name]: { ...current[skill.name], loading: true, error: undefined } }));
      try {
        const loaded = await onReadSkill(skill.name);
        body = loaded.body ?? "";
        setBodyByName((current) => ({ ...current, [skill.name]: { body } }));
      } catch (err) {
        const message = formatPanelError(err);
        setBodyByName((current) => ({ ...current, [skill.name]: { error: message } }));
        setSkillActionStatus({ tone: "error", message });
        return;
      }
    }
    setForm({
      name: skill.name,
      description: skill.description ?? "",
      triggers: (skill.triggers ?? skill.auto_activation?.any ?? []).join("\n"),
      requiredTools: (skill.required_tools ?? []).join("\n"),
      body: body ?? skill.body_preview ?? "",
    });
    setEditingSkillName(skill.name);
    setShowForm(true);
  }

  async function deleteSkill(name: string) {
    if (!onDeleteSkill || deletingSkillName) return;
    setSkillActionStatus(undefined);
    setDeletingSkillName(name);
    try {
      await onDeleteSkill(name);
      setBodyByName((current) => {
        const next = { ...current };
        delete next[name];
        return next;
      });
      setDeleteConfirmName(undefined);
      if (editingSkillName === name) {
        setEditingSkillName(undefined);
        setShowForm(false);
        setForm({ name: "", description: "", triggers: "", requiredTools: "", body: "" });
      }
      setSkillActionStatus({ tone: "success", message: `${name} deleted.` });
    } catch (err) {
      setSkillActionStatus({ tone: "error", message: formatPanelError(err) });
    } finally {
      setDeletingSkillName(undefined);
    }
  }

  return (
    <details
      className="session-skills-panel"
      data-testid="session-skills-panel"
      data-surface={surface ? "true" : undefined}
      open={surface || panelOpen}
      onToggle={(event) => {
        if (surface) {
          event.currentTarget.open = true;
          return;
        }
        setPanelOpen(event.currentTarget.open);
      }}
    >
      <summary className="session-skills-summary" onClick={surface ? (event) => event.preventDefault() : undefined}>
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
        {!loading && error && canDraftSkill ? (
          <>
            <div className="session-runtime-fallback" data-testid="session-skills-fallback">
              <strong>Skills can still be drafted</strong>
              <span>Create a reusable workflow in chat while the Skills API is unavailable.</span>
              <button type="button" className="session-skills-add-toggle" onClick={() => setShowForm((open) => !open)}>
                {showForm ? "Cancel" : "Create skill"}
              </button>
            </div>
            {showForm ? renderSkillForm({
              form,
              setForm,
              submitSkill,
              installError,
              installing,
              submitLabel: "Prepare skill draft",
            }) : null}
          </>
        ) : null}
        {skillActionStatus ? (
          <span className="session-skills-status" data-tone={skillActionStatus.tone} role="status" aria-live="polite">
            {skillActionStatus.message}
          </span>
        ) : null}
        {!loading && !error ? (
          <>
            <SkillsDashboard skills={allSkills} installEnabled={installEnabled} />
            {hasSearch ? (
              <SkillActivationCheck
                value={activationProbe}
                matches={activationMatches}
                onChange={setActivationProbe}
                onFocusSkill={(name) => {
                  setSelectedSkillName(name);
                  setSkillFilter("all");
                  setQuery("");
                }}
                onClear={() => setActivationProbe("")}
              />
            ) : null}
            {hasSkillMaintenanceSignal(allSkills) ? (
              <SkillCoverageMap
                skills={allSkills}
                onFilter={setSkillFilter}
              />
            ) : null}
            {hasSearch || canInstall || onRefresh ? (
              <div className="session-skills-controls">
                {hasSearch ? <SkillFilters skills={allSkills} value={skillFilter} onChange={setSkillFilter} /> : null}
                {hasSearch ? (
                  <label className="session-skills-search">
                    <span>Search skills</span>
                    <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Search title or summary" />
                  </label>
                ) : null}
                {trimmedQuery || skillFilter !== "all" ? (
                  <button type="button" className="ghost-action" onClick={() => {
                    setQuery("");
                    setSkillFilter("all");
                  }}>
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
                {trimmedQuery || skillFilter !== "all" ? (
                  <span className="session-search-count" data-testid="session-skills-search-count">
                    {filteredSkills.length} {filteredSkills.length === 1 ? "skill" : "skills"}
                    {trimmedQuery ? ` matching "${trimmedQuery}"` : ""}
                  </span>
                ) : null}
              </div>
            ) : null}
            {showForm ? (
              renderSkillForm({
                form,
                setForm,
                submitSkill,
                installError,
                installing,
                editingSkillName,
                onCancelEdit: editingSkillName ? () => {
                  setEditingSkillName(undefined);
                  setShowForm(false);
                  setForm({ name: "", description: "", triggers: "", requiredTools: "", body: "" });
                } : undefined,
                submitLabel: editingSkillName ? "Update skill" : "Save skill",
              })
            ) : null}
            {focusedSkill ? (
              <SkillReviewFocus
                skill={focusedSkill}
                bodyState={bodyByName[focusedSkill.name]}
                onLoadBody={onReadSkill ? () => void loadBody(focusedSkill.name) : undefined}
                onEdit={canInstall && focusedSkill.runtime ? () => void editSkill(focusedSkill) : undefined}
                onUseAsDraft={onUseAsDraft}
              />
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
                      data-selected={focusedSkill?.name === skill.name ? "true" : "false"}
                      open={trimmedQuery ? true : undefined}
                      onToggle={(event) => {
                        if (event.currentTarget.open) setSelectedSkillName(skill.name);
                        if (event.currentTarget.open && !trimmedQuery) void loadBody(skill.name);
                      }}
                    >
                      <summary onClick={() => setSelectedSkillName(skill.name)}>
                        <span className="session-skill-title">
                          <strong>{skill.name}</strong>
                          <span>{skillKindLabel(skill)}</span>
                        </span>
                        <span className="session-skill-desc">
                          <span>{skill.description || "No summary"}</span>
                          {activationSummary(skill) ? <small data-testid={`skill-activation-${skill.name}`}>{activationSummary(skill)}</small> : null}
                        </span>
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
                          {skillOriginLabel(skill) ? <span>Origin: {skillOriginLabel(skill)}</span> : null}
                          <span>{skillSizeLabel(skill)}</span>
                          {activationSummary(skill) ? <span>{activationSummary(skill)}</span> : null}
                        </div>
                        <div className="session-skill-actions">
                          <CopyButton label="Copy details" value={skillEvidenceText(skill, body)} className="node-action" />
                          {canInstall && skill.runtime ? (
                            <button type="button" className="node-action" onClick={() => void editSkill(skill)}>
                              Edit
                            </button>
                          ) : null}
                          {onDeleteSkill && skill.runtime ? (
                            deleteConfirmName === skill.name ? (
                              <span className="session-skill-delete-confirm" role="group" aria-label={`Confirm delete ${skill.name}`}>
                                <button type="button" className="node-action" disabled={deletingSkillName === skill.name} onClick={() => setDeleteConfirmName(undefined)}>
                                  Cancel
                                </button>
                                <button type="button" className="node-action danger-action" disabled={!!deletingSkillName} onClick={() => void deleteSkill(skill.name)}>
                                  {deletingSkillName === skill.name ? "Deleting" : "Confirm delete"}
                                </button>
                              </span>
                            ) : (
                              <button type="button" className="node-action danger-action" disabled={!!deletingSkillName} onClick={() => setDeleteConfirmName(skill.name)}>
                                Delete
                              </button>
                            )
                          ) : null}
                          {onUseAsDraft ? (
                            <>
                              <button type="button" className="node-action" onClick={() => onUseAsDraft(skillDraft(skill, body), "skill")}>
                                Start from skill
                              </button>
                              <button type="button" className="node-action" onClick={() => onUseAsDraft(skillUpdateDraft(skill, body), "skill")}>
                                Revise skill
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
                          <>
                            {onReadSkill ? (
                              <button type="button" className="ghost-action" onClick={() => void loadBody(skill.name)}>
                                Load full content
                              </button>
                            ) : null}
                            <p className="session-skill-preview">{skill.body_preview || "No content preview."}</p>
                          </>
                        ) : null}
                      </div>
                    </details>
                  );
                })
              ) : (
                <>
                  <div className="session-skills-empty">{allSkills.length > 0 ? "No matching skills." : emptySkillsText(canInstall)}</div>
                  {allSkills.length > 0 && canInstall && trimmedQuery ? (
                    <button
                      type="button"
                      className="session-skills-add-toggle"
                      onClick={() => {
                        const name = skillNameFromQuery(trimmedQuery);
                        setForm({
                          name,
                          description: `Reusable workflow for ${trimmedQuery}`,
                          triggers: trimmedQuery,
                          requiredTools: "",
                          body: "",
                        });
                        setEditingSkillName(undefined);
                        setInstallError(undefined);
                        setShowForm(true);
                        setSkillActionStatus({ tone: "success", message: `Draft ${name}; fill full content before saving.` });
                      }}
                    >
                      Draft matching skill
                    </button>
                  ) : null}
                </>
              )}
            </div>
          </>
        ) : null}
      </div>
    </details>
  );
}

type SkillFilter = "all" | "custom" | "built-in" | "triggerable" | "tool-bound" | "manual" | "needs-summary";

function SkillFilters({
  skills,
  value,
  onChange,
}: {
  skills: readonly SessionSkillInfo[];
  value: SkillFilter;
  onChange: (value: SkillFilter) => void;
}) {
  const counts = {
    all: skills.length,
    custom: skills.filter((skill) => skill.runtime).length,
    "built-in": skills.filter((skill) => !skill.runtime).length,
    triggerable: skills.filter((skill) => skillTriggers(skill).length > 0).length,
    "tool-bound": skills.filter((skill) => (skill.required_tools?.length ?? 0) > 0).length,
    manual: skills.filter((skill) => skillTriggers(skill).length === 0).length,
    "needs-summary": skills.filter((skill) => !skill.description?.trim()).length,
  };
  const options: Array<{ value: SkillFilter; label: string; count: number }> = [
    { value: "all", label: "All", count: counts.all },
    { value: "custom", label: "Custom", count: counts.custom },
    { value: "built-in", label: "Built in", count: counts["built-in"] },
    { value: "triggerable", label: "Triggerable", count: counts.triggerable },
    { value: "tool-bound", label: "Tool-bound", count: counts["tool-bound"] },
    { value: "manual", label: "Manual-only", count: counts.manual },
    { value: "needs-summary", label: "Needs summary", count: counts["needs-summary"] },
  ];
  return (
    <div className="session-filter-pills" role="group" aria-label="Filter skills">
      {options.map((option) => (
        <button
          key={option.value}
          type="button"
          aria-pressed={value === option.value}
          disabled={option.count === 0 && value !== option.value}
          onClick={() => onChange(option.value)}
        >
          <span>{option.label}</span>
          <strong>{option.count}</strong>
        </button>
      ))}
    </div>
  );
}

function skillMatchesFilter(skill: SessionSkillInfo, filter: SkillFilter): boolean {
  if (filter === "custom") return Boolean(skill.runtime);
  if (filter === "built-in") return !skill.runtime;
  if (filter === "triggerable") return skillTriggers(skill).length > 0;
  if (filter === "tool-bound") return (skill.required_tools?.length ?? 0) > 0;
  if (filter === "manual") return skillTriggers(skill).length === 0;
  if (filter === "needs-summary") return !skill.description?.trim();
  return true;
}

function hasSkillMaintenanceSignal(skills: readonly SessionSkillInfo[]): boolean {
  if (skills.length === 0) return false;
  return skills.some((skill) => skill.runtime || skillTriggers(skill).length === 0 || !skill.description?.trim());
}

function SkillCoverageMap({ skills, onFilter }: { skills: readonly SessionSkillInfo[]; onFilter: (filter: SkillFilter) => void }) {
  const triggerable = skills.filter((skill) => skillTriggers(skill).length > 0).length;
  const manual = skills.length - triggerable;
  const missingSummary = skills.filter((skill) => !skill.description?.trim()).length;
  const custom = skills.filter((skill) => skill.runtime).length;
  const triggerPercent = skills.length > 0 ? Math.round((triggerable / skills.length) * 100) : 0;
  const tools = topCounts(skills.flatMap((skill) => skill.required_tools ?? []));
  const triggers = topCounts(skills.flatMap((skill) => skillTriggers(skill)));
  return (
    <section className="session-skills-coverage" data-testid="session-skills-coverage" aria-label="Skills coverage">
      <div className="session-skills-coverage-main">
        <div className="session-skills-coverage-head">
          <span>Skill maintenance</span>
          <strong>{triggerable}/{skills.length}</strong>
        </div>
        <div className="session-skills-bar" aria-label={`${triggerPercent}% triggerable`}>
          <span style={{ width: `${triggerPercent}%` }} />
        </div>
        <div className="session-skills-coverage-actions">
          <button type="button" onClick={() => onFilter("triggerable")}>Show triggerable</button>
          <button type="button" onClick={() => onFilter("manual")} disabled={manual === 0}>Manual-only {manual}</button>
          <button type="button" onClick={() => onFilter("custom")} disabled={custom === 0}>Custom {custom}</button>
          <button type="button" onClick={() => onFilter("needs-summary")} disabled={missingSummary === 0}>Needs summary {missingSummary}</button>
        </div>
      </div>
      <SkillCoverageChips title="Top triggers" empty="No trigger rules" items={triggers} />
      <SkillCoverageChips title="Tool surface" empty="No tool requirements" items={tools} />
    </section>
  );
}

function SkillCoverageChips({ title, empty, items }: { title: string; empty: string; items: Array<{ name: string; count: number }> }) {
  return (
    <div className="session-skills-coverage-group">
      <span>{title}</span>
      <div>
        {items.length > 0 ? items.slice(0, 8).map((item) => (
          <small key={item.name} title={`${item.name}: ${item.count}`}>
            {item.name}
            <strong>{item.count}</strong>
          </small>
        )) : <em>{empty}</em>}
      </div>
    </div>
  );
}

function SkillActivationCheck({
  value,
  matches,
  onChange,
  onFocusSkill,
  onClear,
}: {
  value: string;
  matches: ReturnType<typeof matchingSkillsForPrompt>;
  onChange: (value: string) => void;
  onFocusSkill: (name: string) => void;
  onClear: () => void;
}) {
  const trimmed = value.trim();
  return (
    <section className="session-skills-activation" data-testid="session-skills-activation" aria-label="Skill activation check">
      <div className="session-skills-activation-head">
        <div>
          <span>Activation check</span>
          <strong>{trimmed ? `${matches.length} ${matches.length === 1 ? "match" : "matches"}` : "Test a task"}</strong>
        </div>
        {trimmed ? (
          <button type="button" className="ghost-action" onClick={onClear}>
            Clear
          </button>
        ) : null}
      </div>
      <label className="session-skills-activation-input">
        <span>Task text</span>
        <input
          value={value}
          onChange={(event) => onChange(event.target.value)}
          placeholder="Paste a task to see which skills would activate"
        />
      </label>
      {trimmed ? (
        matches.length > 0 ? (
          <div className="session-skills-activation-matches">
            {matches.slice(0, 6).map((match) => (
              <button key={match.skill.name} type="button" onClick={() => onFocusSkill(match.skill.name)} title={match.reason}>
                <strong>{match.skill.name}</strong>
                <span>{match.reason}</span>
              </button>
            ))}
          </div>
        ) : (
          <p className="session-skills-activation-empty">No automatic skill match for this task.</p>
        )
      ) : null}
    </section>
  );
}

function topCounts(values: readonly string[]): Array<{ name: string; count: number }> {
  const counts = new Map<string, number>();
  values.forEach((raw) => {
    const value = raw.trim();
    if (!value) return;
    counts.set(value, (counts.get(value) ?? 0) + 1);
  });
  return Array.from(counts, ([name, count]) => ({ name, count }))
    .sort((left, right) => right.count - left.count || left.name.localeCompare(right.name));
}

function SkillReviewFocus({
  skill,
  bodyState,
  onLoadBody,
  onEdit,
  onUseAsDraft,
}: {
  skill: SessionSkillInfo;
  bodyState?: SkillBodyState;
  onLoadBody?: () => void;
  onEdit?: () => void;
  onUseAsDraft?: UseAsDraft;
}) {
  const body = bodyState?.body ?? skill.body;
  const triggers = skillTriggers(skill);
  const requiredTools = skill.required_tools ?? [];
  const preview = skill.body_preview || body;
  const contentState = body ? "Loaded" : onLoadBody ? "Available" : preview ? "Preview only" : "Unavailable";
  const maintenanceDetail = skill.runtime
    ? "custom skill can be edited"
    : "built-in skill is read-only";
  return (
    <section className="session-skills-focus" data-testid="session-skills-focus" aria-label={`Skill review ${skill.name}`}>
      <div className="session-skills-focus-head">
        <span>{skillKindLabel(skill)}</span>
        <strong>{skill.name}</strong>
        <small>{skill.description || "No summary"}</small>
      </div>
      <div className="session-skill-actions">
        <CopyButton label="Copy details" value={skillEvidenceText(skill, body)} className="node-action" />
        {onLoadBody && !body ? (
          <button type="button" className="node-action" onClick={onLoadBody}>
            Load content
          </button>
        ) : null}
        {onEdit ? (
          <button type="button" className="node-action" onClick={onEdit}>
            Edit skill
          </button>
        ) : null}
        {onUseAsDraft ? (
          <>
            <button type="button" className="node-action" onClick={() => onUseAsDraft(skillDraft(skill, body), "skill")}>
              Start from skill
            </button>
            <button type="button" className="node-action" onClick={() => onUseAsDraft(skillUpdateDraft(skill, body), "skill")}>
              Revise skill
            </button>
          </>
        ) : null}
      </div>
      <div className="session-skills-focus-grid">
        <SkillFocusFact label="Source" value={skillOriginLabel(skill) ?? "Runtime"} />
        <SkillFocusFact label="Size" value={skillSizeLabel(skill)} />
        <SkillFocusFact label="Triggers" value={triggers.length > 0 ? `${triggers.length}` : "None"} detail={triggers.slice(0, 5).join(", ")} />
        <SkillFocusFact label="Tools" value={requiredTools.length > 0 ? `${requiredTools.length}` : "None"} detail={requiredTools.join(", ")} />
      </div>
      {triggers.length > 0 || requiredTools.length > 0 ? (
        <div className="session-skills-focus-chips">
          {triggers.slice(0, 8).map((trigger) => <span key={`trigger:${trigger}`}>trigger:{trigger}</span>)}
          {requiredTools.map((tool) => <span key={`tool:${tool}`} data-kind="tool">tool:{tool}</span>)}
        </div>
      ) : null}
      <div className="session-skills-focus-body">
        <span>Maintenance</span>
        <div className="session-skills-maintenance-grid">
          <SkillFocusFact label="Full content" value={contentState} detail={body ? "loaded for review actions" : onLoadBody ? "load only when needed" : undefined} />
          <SkillFocusFact label="Editable" value={onEdit ? "Yes" : "No"} detail={maintenanceDetail} />
        </div>
        {bodyState?.loading ? <p>Loading full content...</p> : null}
        {bodyState?.error ? <p className="error">{bodyState.error}</p> : null}
        {!bodyState?.loading && !bodyState?.error && preview ? <p>{summarizeSkillPreview(preview)}</p> : null}
      </div>
    </section>
  );
}

function SkillFocusFact({ label, value, detail }: { label: string; value: string; detail?: string }) {
  return (
    <div className="session-skills-focus-fact">
      <span>{label}</span>
      <strong title={detail || value}>{value}</strong>
      {detail ? <small title={detail}>{detail}</small> : null}
    </div>
  );
}

function summarizeSkillPreview(value: string): string {
  const normalized = value
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean)
    .filter((line) => !/^AFFENT ACTIVE SKILL\b/i.test(line))
    .slice(0, 3)
    .join(" ");
  if (!normalized) return "No content preview.";
  return normalized.length > 180 ? `${normalized.slice(0, 179).trimEnd()}...` : normalized;
}

function SkillsDashboard({ skills, installEnabled }: { skills: readonly SessionSkillInfo[]; installEnabled: boolean }) {
  const runtime = skills.filter((skill) => skill.runtime).length;
  const builtIn = skills.length - runtime;
  const triggerable = skills.filter((skill) => (skill.triggers?.length ?? skill.auto_activation?.any?.length ?? 0) > 0).length;
  const manualOnly = skills.filter((skill) => skillTriggers(skill).length === 0).length;
  const missingSummary = skills.filter((skill) => !skill.description?.trim()).length;
  const reviewCount = manualOnly + missingSummary;
  return (
    <div className="session-skills-dashboard" data-testid="session-skills-dashboard">
      <div className="session-skills-stat">
        <span>Available</span>
        <strong>{skills.length}</strong>
        <small>{builtIn} built in · {runtime} custom</small>
      </div>
      <div className="session-skills-stat">
        <span>Activation</span>
        <strong>{triggerable}</strong>
        <small>{triggerable === 1 ? "triggerable skill" : "triggerable skills"}</small>
      </div>
      <div className="session-skills-stat" data-tone={reviewCount > 0 ? "action" : "normal"}>
        <span>Review</span>
        <strong>{reviewCount}</strong>
        <small>{skillsReviewSummary(manualOnly, missingSummary)}</small>
      </div>
      <div className="session-skills-stat">
        <span>Install</span>
        <strong>{installEnabled ? "Enabled" : "Read only"}</strong>
        <small>{installEnabled ? "custom skills allowed" : "runtime install disabled"}</small>
      </div>
    </div>
  );
}

function skillsReviewSummary(manualOnly: number, missingSummary: number): string {
  if (manualOnly === 0 && missingSummary === 0) return "No review gaps";
  return [
    manualOnly > 0 ? `Manual-only ${manualOnly}` : undefined,
    missingSummary > 0 ? `No summary ${missingSummary}` : undefined,
  ].filter((item): item is string => Boolean(item)).join(" · ");
}

type SkillFormState = { name: string; description: string; triggers: string; requiredTools: string; body: string };

function renderSkillForm({
  form,
  setForm,
  submitSkill,
  installError,
  installing,
  editingSkillName,
  onCancelEdit,
  submitLabel,
}: {
  form: SkillFormState;
  setForm: (updater: (current: SkillFormState) => SkillFormState) => void;
  submitSkill: (event: FormEvent<HTMLFormElement>) => void;
  installError?: string;
  installing: boolean;
  editingSkillName?: string;
  onCancelEdit?: () => void;
  submitLabel: string;
}) {
  return (
    <form className="session-skill-form" onSubmit={submitSkill}>
      {editingSkillName ? (
        <div className="session-skill-editing" role="status">
          Editing {editingSkillName}
          {onCancelEdit ? (
            <button type="button" className="ghost-action" onClick={onCancelEdit}>
              Cancel edit
            </button>
          ) : null}
        </div>
      ) : null}
      <label>
        <span>Name</span>
        <input value={form.name} onChange={(event) => setForm((current) => ({ ...current, name: event.target.value }))} placeholder="my_skill" required disabled={!!editingSkillName} />
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
      <button type="submit" className="session-skills-add-submit" disabled={installing || !form.name.trim() || !form.body.trim()}>
        {installing ? "Adding" : submitLabel}
      </button>
    </form>
  );
}

function manualSkillDraft(form: SkillFormState): string {
  return [
    "Create or update this reusable skill when the Skills API is available:",
    `Name: ${form.name.trim()}`,
    form.description.trim() ? `Summary: ${form.description.trim()}` : undefined,
    splitList(form.triggers)?.length ? `Triggers: ${splitList(form.triggers)?.join(", ")}` : undefined,
    splitList(form.requiredTools)?.length ? `Required tools: ${splitList(form.requiredTools)?.join(", ")}` : undefined,
    "",
    "Content:",
    form.body.trim(),
  ].filter((line): line is string => line != null).join("\n");
}

function splitList(text: string): string[] | undefined {
  const parts = text
    .split(/[,\n]/)
    .map((part) => part.trim())
    .filter(Boolean);
  return parts.length > 0 ? parts : undefined;
}

function skillNameFromQuery(query: string): string {
  const slug = query
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "_")
    .replace(/^_+|_+$/g, "")
    .slice(0, 48);
  return slug || "new_skill";
}

function emptySkillsText(canInstall: boolean): string {
  return canInstall ? "No reusable workflows saved yet." : "No skills returned by this runtime.";
}

function formatPanelError(err: unknown): string {
  if (err instanceof Error) return err.message;
  return "Unknown error";
}
