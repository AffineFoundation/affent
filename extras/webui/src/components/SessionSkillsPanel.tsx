import { useMemo, useState, type FormEvent } from "react";
import type {
  SessionSkillInfo,
  SessionSkillInstallRequest,
} from "../api/sessions";
import type { UseAsDraft } from "../view/draftSource";
import {
  matchingSkillsForPrompt,
  skillKindLabel,
  skillMatchesQuery,
  skillOriginLabel,
  skillSearchMatches,
  skillSizeLabel,
  skillSummaryTags,
  skillTriggers,
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
  onInstallSkill?: (
    request: SessionSkillInstallRequest,
  ) => Promise<SessionSkillInfo>;
  onDeleteSkill?: (name: string) => Promise<void> | void;
  onUseAsDraft?: UseAsDraft;
}) {
  const [query, setQuery] = useState("");
  const [bodyByName, setBodyByName] = useState<Record<string, SkillBodyState>>(
    {},
  );
  const [panelOpen, setPanelOpen] = useState(defaultOpen);
  const [showForm, setShowForm] = useState(false);
  const [form, setForm] = useState({
    name: "",
    description: "",
    triggers: "",
    requiredTools: "",
    body: "",
  });
  const [installError, setInstallError] = useState<string | undefined>();
  const [skillActionStatus, setSkillActionStatus] = useState<
    { tone: "success" | "error"; message: string } | undefined
  >();
  const [installing, setInstalling] = useState(false);
  const [editingSkillName, setEditingSkillName] = useState<
    string | undefined
  >();
  const [deleteConfirmName, setDeleteConfirmName] = useState<
    string | undefined
  >();
  const [deletingSkillName, setDeletingSkillName] = useState<
    string | undefined
  >();
  const [selectedSkillName, setSelectedSkillName] = useState<
    string | undefined
  >();
  const [skillFilter, setSkillFilter] = useState<SkillFilter>("all");
  const [activationProbe, setActivationProbe] = useState("");
  const [optimisticSkills, setOptimisticSkills] = useState<SessionSkillInfo[]>(
    [],
  );
  const allSkills = useMemo(() => {
    const names = new Set(optimisticSkills.map((skill) => skill.name));
    return [
      ...optimisticSkills,
      ...(skills ?? []).filter((skill) => !names.has(skill.name)),
    ];
  }, [optimisticSkills, skills]);
  const hasSearch = allSkills.length > 0;
  const canInstall = installEnabled && !!onInstallSkill;
  const trimmedQuery = query.trim();
  const filteredSkills = useMemo(() => {
    return allSkills
      .filter((skill) => skillMatchesFilter(skill, skillFilter))
      .filter(
        (skill) => !trimmedQuery || skillMatchesQuery(skill, trimmedQuery),
      );
  }, [allSkills, skillFilter, trimmedQuery]);
  const activationMatches = useMemo(
    () => matchingSkillsForPrompt(allSkills, activationProbe),
    [activationProbe, allSkills],
  );
  const activationDiagnostics = useMemo(
    () => skillActivationDiagnostics(allSkills, activationProbe),
    [activationProbe, allSkills],
  );
  const focusedSkill = useMemo(() => {
    if (filteredSkills.length === 0) return undefined;
    const selected = selectedSkillName
      ? filteredSkills.find((skill) => skill.name === selectedSkillName)
      : undefined;
    if (selected) return selected;
    return (
      filteredSkills.find(skillNeedsReview) ??
      filteredSkills.find((skill) => skill.runtime) ??
      filteredSkills.find((skill) => (skill.required_tools?.length ?? 0) > 0) ??
      filteredSkills[0]
    );
  }, [filteredSkills, selectedSkillName]);
  const runtimeCount = allSkills.filter((skill) => skill.runtime).length;
  const reviewCount = allSkills.filter(skillNeedsReview).length;
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
        ? ""
        : reviewCount > 0
          ? "Maintenance queue"
          : runtimeCount > 0
            ? `${runtimeCount} custom · ${allSkills.length - runtimeCount} built in`
            : "";

  async function loadBody(name: string) {
    if (!onReadSkill || bodyByName[name]?.body || bodyByName[name]?.loading)
      return;
    setBodyByName((current) => ({
      ...current,
      [name]: { ...current[name], loading: true, error: undefined },
    }));
    try {
      const skill = await onReadSkill(name);
      setBodyByName((current) => ({
        ...current,
        [name]: { body: skill.body ?? "" },
      }));
    } catch (err) {
      setBodyByName((current) => ({
        ...current,
        [name]: { error: formatPanelError(err) },
      }));
    }
  }

  async function submitSkill(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (installing) return;
    if (!onInstallSkill) {
      if (onUseAsDraft && form.name.trim() && form.body.trim()) {
        setSkillActionStatus(undefined);
        onUseAsDraft(manualSkillDraft(form), "skill");
        setForm({
          name: "",
          description: "",
          triggers: "",
          requiredTools: "",
          body: "",
        });
        setShowForm(false);
        setSkillActionStatus({
          tone: "success",
          message: `${form.name.trim()} draft prepared.`,
        });
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
      setBodyByName((current) => ({
        ...current,
        [installed.name]: { body: installed.body ?? form.body },
      }));
      setOptimisticSkills((current) => [
        installed,
        ...current.filter((skill) => skill.name !== installed.name),
      ]);
      setSelectedSkillName(installed.name);
      setSkillFilter("all");
      setQuery("");
      setForm({
        name: "",
        description: "",
        triggers: "",
        requiredTools: "",
        body: "",
      });
      setEditingSkillName(undefined);
      setShowForm(false);
      setSkillActionStatus({
        tone: "success",
        message: `${installed.name} saved.`,
      });
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
      setBodyByName((current) => ({
        ...current,
        [skill.name]: {
          ...current[skill.name],
          loading: true,
          error: undefined,
        },
      }));
      try {
        const loaded = await onReadSkill(skill.name);
        body = loaded.body ?? "";
        setBodyByName((current) => ({ ...current, [skill.name]: { body } }));
      } catch (err) {
        const message = formatPanelError(err);
        setBodyByName((current) => ({
          ...current,
          [skill.name]: { error: message },
        }));
        setSkillActionStatus({ tone: "error", message });
        return;
      }
    }
    setForm({
      name: skill.name,
      description: skill.description ?? "",
      triggers: (skill.triggers ?? skill.auto_activation?.any ?? []).join(", "),
      requiredTools: (skill.required_tools ?? []).join(", "),
      body: body ?? skill.body_preview ?? "",
    });
    setEditingSkillName(skill.name);
    setShowForm(true);
  }

  async function cloneSkill(skill: SessionSkillInfo) {
    setInstallError(undefined);
    setSkillActionStatus(undefined);
    setDeleteConfirmName(undefined);
    let body = bodyByName[skill.name]?.body ?? skill.body;
    if (!body && onReadSkill) {
      setBodyByName((current) => ({
        ...current,
        [skill.name]: {
          ...current[skill.name],
          loading: true,
          error: undefined,
        },
      }));
      try {
        const loaded = await onReadSkill(skill.name);
        body = loaded.body ?? "";
        setBodyByName((current) => ({ ...current, [skill.name]: { body } }));
      } catch (err) {
        const message = formatPanelError(err);
        setBodyByName((current) => ({
          ...current,
          [skill.name]: { error: message },
        }));
        setSkillActionStatus({ tone: "error", message });
        return;
      }
    }
    const cloneName = clonedSkillName(skill.name, allSkills);
    setForm({
      name: cloneName,
      description: skill.description ?? "",
      triggers: (skill.triggers ?? skill.auto_activation?.any ?? []).join(", "),
      requiredTools: (skill.required_tools ?? []).join(", "),
      body: body ?? skill.body_preview ?? "",
    });
    setEditingSkillName(undefined);
    setShowForm(true);
    setSkillActionStatus({
      tone: "success",
      message: `Cloned ${skill.name}; review before saving ${cloneName}.`,
    });
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
      setOptimisticSkills((current) =>
        current.filter((skill) => skill.name !== name),
      );
      setSelectedSkillName((current) =>
        current === name ? undefined : current,
      );
      setDeleteConfirmName(undefined);
      if (editingSkillName === name) {
        setEditingSkillName(undefined);
        setShowForm(false);
        setForm({
          name: "",
          description: "",
          triggers: "",
          requiredTools: "",
          body: "",
        });
      }
      setSkillActionStatus({ tone: "success", message: `${name} deleted.` });
    } catch (err) {
      setSkillActionStatus({ tone: "error", message: formatPanelError(err) });
    } finally {
      setDeletingSkillName(undefined);
    }
  }

  function openSkillReview(kind: SkillReviewKind) {
    const target =
      allSkills.find((skill) =>
        skillReviewIssues(skill).some((issue) => issue.kind === kind),
      ) ?? allSkills.find(skillNeedsReview);
    setSkillFilter("needs-review");
    setQuery("");
    setShowForm(false);
    setDeleteConfirmName(undefined);
    setEditingSkillName(undefined);
    if (target) setSelectedSkillName(target.name);
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
      <summary
        className="session-skills-summary"
        onClick={surface ? (event) => event.preventDefault() : undefined}
      >
        <span className="session-skills-kicker">Skills</span>
        <strong>{summary}</strong>
        <span>{summaryDetail}</span>
      </summary>
      <div className="session-skills-body">
        {loading ? (
          <div className="session-skills-empty">Loading account skills...</div>
        ) : null}
        {!loading && error ? (
          <div className="session-skills-empty error" role="alert">
            {error}
            {onRefresh ? (
              <button
                type="button"
                className="ghost-action"
                onClick={() => void onRefresh()}
              >
                Retry
              </button>
            ) : null}
          </div>
        ) : null}
        {!loading && error && canDraftSkill ? (
          <>
            <div
              className="session-runtime-fallback"
              data-testid="session-skills-fallback"
            >
              <strong>Skills can still be drafted</strong>
              <span>
                Create a reusable workflow in chat while the Skills API is
                unavailable.
              </span>
              <button
                type="button"
                className="session-skills-add-toggle"
                onClick={() => setShowForm((open) => !open)}
              >
                {showForm ? "Cancel" : "Create skill"}
              </button>
            </div>
            {showForm
              ? renderSkillForm({
                  form,
                  setForm,
                  submitSkill,
                  installError,
                  installing,
                  submitLabel: "Prepare skill draft",
                })
              : null}
          </>
        ) : null}
        {skillActionStatus ? (
          <span
            className="session-skills-status"
            data-tone={skillActionStatus.tone}
            role="status"
            aria-live="polite"
          >
            {skillActionStatus.message}
          </span>
        ) : null}
        {!loading && !error ? (
          <>
            {hasSearch ? (
              <SkillActivationCheck
                value={activationProbe}
                matches={activationMatches}
                diagnostics={activationDiagnostics}
                onChange={setActivationProbe}
                onFocusSkill={(name) => {
                  setSelectedSkillName(name);
                  setSkillFilter("all");
                  setQuery("");
                }}
                onClear={() => setActivationProbe("")}
              />
            ) : null}
            <SkillWorkbenchToolbar
              skills={allSkills}
              filter={skillFilter}
              query={query}
              filteredCount={filteredSkills.length}
              canInstall={canInstall}
              canRefresh={Boolean(onRefresh)}
              showForm={showForm}
              onFilterChange={setSkillFilter}
              onQueryChange={setQuery}
              onClear={() => {
                setQuery("");
                setSkillFilter("all");
              }}
              onToggleForm={() => setShowForm((open) => !open)}
              onRefresh={onRefresh}
              onOpenReview={openSkillReview}
            />
            <div className="session-skills-manager">
              <aside
                className="session-skills-sidebar"
                aria-label="Skill workflows"
              >
                <SkillWorkflowList
                  skills={filteredSkills}
                  allSkillCount={allSkills.length}
                  selectedSkillName={focusedSkill?.name}
                  query={trimmedQuery}
                  draftPath={showForm ? skillFormPath(form.name) : undefined}
                  editingSkillName={editingSkillName}
                  canInstall={canInstall}
                  canDraft={Boolean(
                    allSkills.length > 0 && canInstall && trimmedQuery,
                  )}
                  onSelect={(skill) => {
                    setSelectedSkillName(skill.name);
                    void loadBody(skill.name);
                  }}
                  onDraftFromQuery={() => {
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
                    setSkillActionStatus({
                      tone: "success",
                      message: `Draft ${name}; fill full content before saving.`,
                    });
                  }}
                />
              </aside>
              <section className="session-skills-main">
                {showForm
                  ? renderSkillForm({
                      form,
                      setForm,
                      submitSkill,
                      installError,
                      installing,
                      editingSkillName,
                      onCancelEdit: editingSkillName
                        ? () => {
                            setEditingSkillName(undefined);
                            setShowForm(false);
                            setForm({
                              name: "",
                              description: "",
                              triggers: "",
                              requiredTools: "",
                              body: "",
                            });
                          }
                        : undefined,
                      submitLabel: editingSkillName
                        ? "Update skill"
                        : "Save skill",
                    })
                  : null}
                {focusedSkill && !showForm ? (
                  <SkillReviewFocus
                    skill={focusedSkill}
                    bodyState={bodyByName[focusedSkill.name]}
                    onLoadBody={
                      onReadSkill
                        ? () => void loadBody(focusedSkill.name)
                        : undefined
                    }
                    onEdit={
                      canInstall && focusedSkill.runtime
                        ? () => void editSkill(focusedSkill)
                        : undefined
                    }
                    onClone={
                      canInstall
                        ? () => void cloneSkill(focusedSkill)
                        : undefined
                    }
                    deleteConfirmName={deleteConfirmName}
                    deletingSkillName={deletingSkillName}
                    onAskDelete={
                      onDeleteSkill && focusedSkill.runtime
                        ? () => setDeleteConfirmName(focusedSkill.name)
                        : undefined
                    }
                    onCancelDelete={() => setDeleteConfirmName(undefined)}
                    onConfirmDelete={
                      onDeleteSkill && focusedSkill.runtime
                        ? () => void deleteSkill(focusedSkill.name)
                        : undefined
                    }
                  />
                ) : null}
              </section>
            </div>
          </>
        ) : null}
      </div>
    </details>
  );
}

type SkillFilter = "all" | "custom" | "built-in" | "needs-review";

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
    "needs-review": skills.filter(skillNeedsReview).length,
  };
  const options: Array<{ value: SkillFilter; label: string; count: number }> = [
    { value: "all", label: "All", count: counts.all },
    { value: "custom", label: "Custom", count: counts.custom },
    { value: "built-in", label: "Built in", count: counts["built-in"] },
    {
      value: "needs-review",
      label: "Review",
      count: counts["needs-review"],
    },
  ];
  return (
    <div
      className="session-filter-pills"
      role="group"
      aria-label="Filter skills"
    >
      {options
        .filter(
          (option) =>
            option.value === "all" ||
            option.count > 0 ||
            value === option.value,
        )
        .map((option) => (
          <button
            key={option.value}
            type="button"
            aria-pressed={value === option.value}
            aria-label={`${option.label}: ${option.count}`}
            disabled={option.count === 0 && value !== option.value}
            onClick={() => onChange(option.value)}
          >
            <span>{option.label}</span>
          </button>
        ))}
    </div>
  );
}

function skillMatchesFilter(
  skill: SessionSkillInfo,
  filter: SkillFilter,
): boolean {
  if (filter === "custom") return Boolean(skill.runtime);
  if (filter === "built-in") return !skill.runtime;
  if (filter === "needs-review") return skillNeedsReview(skill);
  return true;
}

function skillNeedsReview(skill: SessionSkillInfo): boolean {
  return Boolean(
    skill.runtime &&
    (skillTriggers(skill).length === 0 || !skill.description?.trim()),
  );
}

type SkillReviewKind = "manual" | "summary";

function SkillWorkbenchToolbar({
  skills,
  filter,
  query,
  filteredCount,
  canInstall,
  canRefresh,
  showForm,
  onFilterChange,
  onQueryChange,
  onClear,
  onToggleForm,
  onRefresh,
  onOpenReview,
}: {
  skills: readonly SessionSkillInfo[];
  filter: SkillFilter;
  query: string;
  filteredCount: number;
  canInstall: boolean;
  canRefresh: boolean;
  showForm: boolean;
  onFilterChange: (filter: SkillFilter) => void;
  onQueryChange: (query: string) => void;
  onClear: () => void;
  onToggleForm: () => void;
  onRefresh?: () => Promise<void> | void;
  onOpenReview: (kind: SkillReviewKind) => void;
}) {
  const review = skills.filter(skillNeedsReview).length;
  const manual = skills.filter(
    (skill) => skill.runtime && skillTriggers(skill).length === 0,
  ).length;
  const missingSummary = skills.filter(
    (skill) => skill.runtime && !skill.description?.trim(),
  ).length;
  const trimmedQuery = query.trim();
  const hasSearch = skills.length > 0;
  if (!hasSearch && !canInstall && !canRefresh) return null;
  return (
    <section
      className="session-skills-toolbar"
      data-testid="session-skills-toolbar"
      aria-label="Skills commands"
    >
      {hasSearch ? (
        <SkillFilters
          skills={skills}
          value={filter}
          onChange={onFilterChange}
        />
      ) : null}
      {hasSearch ? (
        <label className="session-skills-search session-skills-toolbar-search">
          <span className="visually-hidden">Search skills</span>
          <input
            value={query}
            onChange={(event) => onQueryChange(event.target.value)}
            placeholder="Search title or summary"
          />
        </label>
      ) : null}
      <div className="session-skills-toolbar-actions">
        {trimmedQuery || filter !== "all" ? (
          <button type="button" className="ghost-action" onClick={onClear}>
            Clear
          </button>
        ) : null}
        {canInstall ? (
          <button
            type="button"
            className="session-skills-add-toggle"
            onClick={onToggleForm}
          >
            {showForm ? "Cancel" : "Add skill"}
          </button>
        ) : null}
        {canRefresh ? (
          <button
            type="button"
            className="ghost-action"
            onClick={() => void onRefresh?.()}
          >
            Refresh
          </button>
        ) : null}
      </div>
      {review > 0 ? (
        <div
          className="session-skills-review-actions"
          data-testid="session-skills-coverage"
          aria-label="Skill maintenance queue"
        >
          <button
            type="button"
            onClick={() => onOpenReview("manual")}
            disabled={manual === 0}
          >
            Fix manual-only {manual}
          </button>
          <button
            type="button"
            onClick={() => onOpenReview("summary")}
            disabled={missingSummary === 0}
          >
            Add summaries {missingSummary}
          </button>
        </div>
      ) : null}
      {trimmedQuery || filter !== "all" ? (
        <span
          className="session-search-count"
          data-testid="session-skills-search-count"
        >
          {filteredCount} {filteredCount === 1 ? "skill" : "skills"}
          {trimmedQuery ? ` matching "${trimmedQuery}"` : ""}
        </span>
      ) : null}
    </section>
  );
}

function SkillWorkflowList({
  skills,
  allSkillCount,
  selectedSkillName,
  query,
  draftPath,
  editingSkillName,
  canInstall,
  canDraft,
  onSelect,
  onDraftFromQuery,
}: {
  skills: readonly SessionSkillInfo[];
  allSkillCount: number;
  selectedSkillName?: string;
  query: string;
  draftPath?: string;
  editingSkillName?: string;
  canInstall: boolean;
  canDraft: boolean;
  onSelect: (skill: SessionSkillInfo) => void;
  onDraftFromQuery: () => void;
}) {
  const displaySkills = useMemo(() => prioritizeSkills(skills), [skills]);
  return (
    <div
      className="session-skills-list session-skills-workflow-list"
      data-testid="session-skills-list"
    >
      {draftPath ? (
        <div
          className="session-skill-draft-row"
          aria-label="Unsaved skill editor"
        >
          <span className="session-skill-row-main">
            <span className="session-skill-row-head">
              <strong title={draftPath}>{draftPath}</strong>
              <span data-tone="draft">Unsaved</span>
            </span>
            <span className="session-skill-desc">
              <span>
                {editingSkillName
                  ? `Editing ${editingSkillName}`
                  : "Review and save this SKILL.md"}
              </span>
            </span>
          </span>
        </div>
      ) : null}
      {displaySkills.length > 0 ? (
        displaySkills.map((skill) => {
          const searchMatches = query ? skillSearchMatches(skill, query) : [];
          const badges = skillWorkflowBadges(skill);
          return (
            <button
              key={skill.name}
              type="button"
              className="session-skill-row"
              data-kind={skill.runtime ? "custom" : "built-in"}
              data-selected={
                selectedSkillName === skill.name ? "true" : "false"
              }
              data-editing={editingSkillName === skill.name ? "true" : "false"}
              data-review={skillNeedsReview(skill) ? "true" : undefined}
              onClick={() => onSelect(skill)}
            >
              <span className="session-skill-row-main">
                <span className="session-skill-row-head">
                  <strong title={skill.name}>{skill.name}</strong>
                  <span>{skillKindLabel(skill)}</span>
                  {selectedSkillName === skill.name ? (
                    <span data-tone="open">Open</span>
                  ) : null}
                  {editingSkillName === skill.name ? (
                    <span data-tone="editing">Editing</span>
                  ) : null}
                </span>
                <span
                  className="session-skill-desc"
                  data-empty={skill.description?.trim() ? "false" : "true"}
                >
                  <span>{skill.description?.trim() || "Summary missing"}</span>
                </span>
              </span>
              <span className="session-skill-status">
                {badges.map((badge) => (
                  <span
                    key={`${badge.tone}:${badge.label}`}
                    data-tone={badge.tone}
                    title={badge.label}
                  >
                    {badge.label}
                  </span>
                ))}
              </span>
              {searchMatches.length > 0 ? (
                <span
                  className="session-skill-matches"
                  data-testid={`skill-search-matches-${skill.name}`}
                >
                  {searchMatches.map((match) => (
                    <span key={match}>{match}</span>
                  ))}
                </span>
              ) : null}
            </button>
          );
        })
      ) : (
        <>
          <div className="session-skills-empty">
            {allSkillCount > 0
              ? "No matching skills."
              : emptySkillsText(canInstall)}
          </div>
          {canDraft ? (
            <button
              type="button"
              className="session-skills-add-toggle"
              onClick={onDraftFromQuery}
            >
              Draft matching skill
            </button>
          ) : null}
        </>
      )}
    </div>
  );
}

type SkillWorkflowBadge = {
  label: string;
  tone: "neutral" | "ready" | "review";
};

function skillWorkflowBadges(skill: SessionSkillInfo): SkillWorkflowBadge[] {
  const badges: SkillWorkflowBadge[] = skillSummaryTags(skill).map((label) => ({
    label,
    tone: "ready",
  }));
  const triggers = skillTriggers(skill);
  if (skill.runtime && !skill.description?.trim())
    badges.unshift({ label: "Needs summary", tone: "review" });
  if (skill.runtime && triggers.length === 0)
    badges.unshift({ label: "No trigger", tone: "review" });
  return badges.length > 0 ? badges : [{ label: "Manual", tone: "neutral" }];
}

function prioritizeSkills(
  skills: readonly SessionSkillInfo[],
): SessionSkillInfo[] {
  return skills
    .map((skill, index) => ({ skill, index }))
    .sort(
      (a, b) =>
        skillPriorityRank(b.skill) - skillPriorityRank(a.skill) ||
        a.index - b.index,
    )
    .map(({ skill }) => skill);
}

function skillPriorityRank(skill: SessionSkillInfo): number {
  if (skillNeedsReview(skill)) return 4;
  if (skill.runtime) return 3;
  if (skillTriggers(skill).length > 0) return 2;
  if ((skill.required_tools?.length ?? 0) > 0) return 1;
  return 0;
}

function SkillActivationCheck({
  value,
  matches,
  diagnostics,
  onChange,
  onFocusSkill,
  onClear,
}: {
  value: string;
  matches: ReturnType<typeof matchingSkillsForPrompt>;
  diagnostics: ReturnType<typeof skillActivationDiagnostics>;
  onChange: (value: string) => void;
  onFocusSkill: (name: string) => void;
  onClear: () => void;
}) {
  const trimmed = value.trim();
  return (
    <section
      className="session-skills-activation"
      data-testid="session-skills-activation"
      aria-label="Skill activation test"
    >
      <div className="session-skills-activation-head">
        <div>
          <span>Activation probe</span>
          <strong>
            {trimmed
              ? `${matches.length} ${matches.length === 1 ? "match" : "matches"}`
              : "Ready"}
          </strong>
        </div>
        {trimmed ? (
          <button type="button" className="ghost-action" onClick={onClear}>
            Clear
          </button>
        ) : null}
      </div>
      <div className="session-skills-activation-body">
        <label className="session-skills-activation-input">
          <span>Task</span>
          <input
            value={value}
            onChange={(event) => onChange(event.target.value)}
            placeholder="e.g. repair failing workspace tests"
          />
        </label>
        {trimmed ? (
          matches.length > 0 ? (
            <div className="session-skills-activation-matches">
              {matches.slice(0, 6).map((match) => (
                <button
                  key={match.skill.name}
                  type="button"
                  onClick={() => onFocusSkill(match.skill.name)}
                  title={match.reason}
                >
                  <strong>{match.skill.name}</strong>
                  <span>{match.reason}</span>
                </button>
              ))}
            </div>
          ) : (
            <div
              className="session-skills-activation-diagnostics"
              data-testid="session-skills-activation-diagnostics"
            >
              <strong>No automatic match</strong>
              {diagnostics.length > 0 ? (
                <ul>
                  {diagnostics.slice(0, 4).map((diagnostic) => (
                    <li key={`${diagnostic.skill.name}:${diagnostic.reason}`}>
                      <button
                        type="button"
                        onClick={() => onFocusSkill(diagnostic.skill.name)}
                      >
                        <span>{diagnostic.skill.name}</span>
                        <small>{diagnostic.reason}</small>
                      </button>
                    </li>
                  ))}
                </ul>
              ) : (
                <p>No skills available to diagnose.</p>
              )}
            </div>
          )
        ) : null}
      </div>
    </section>
  );
}

function skillActivationDiagnostics(
  skills: readonly SessionSkillInfo[],
  prompt: string,
): Array<{ skill: SessionSkillInfo; reason: string }> {
  const lowerPrompt = prompt.trim().toLowerCase();
  if (!lowerPrompt) return [];
  return skills
    .map((skill) => {
      const triggers = skillTriggers(skill);
      if (triggers.length === 0) {
        const tools = skill.required_tools ?? [];
        return {
          skill,
          reason:
            tools.length > 0
              ? `Manual only; required tools do not auto-activate (${tools.slice(0, 3).join(", ")}).`
              : "Manual only; add triggers to auto-activate.",
        };
      }
      return {
        skill,
        reason: `No trigger matched: ${triggers.slice(0, 4).join(", ")}${triggers.length > 4 ? "..." : ""}.`,
      };
    })
    .slice(0, 6);
}

function SkillReviewFocus({
  skill,
  bodyState,
  onLoadBody,
  onEdit,
  onClone,
  deleteConfirmName,
  deletingSkillName,
  onAskDelete,
  onCancelDelete,
  onConfirmDelete,
}: {
  skill: SessionSkillInfo;
  bodyState?: SkillBodyState;
  onLoadBody?: () => void;
  onEdit?: () => void;
  onClone?: () => void;
  deleteConfirmName?: string;
  deletingSkillName?: string;
  onAskDelete?: () => void;
  onCancelDelete: () => void;
  onConfirmDelete?: () => void;
}) {
  const body = bodyState?.body ?? skill.body;
  const triggers = skillTriggers(skill);
  const requiredTools = skill.required_tools ?? [];
  const preview = skill.body_preview || body;
  const reviewIssues = skillReviewIssues(skill);
  const origin =
    skillOriginLabel(skill) ??
    (skill.runtime ? "Account skill" : "Built-in library");
  const activationSummaryText = [
    triggers.length > 0
      ? `${triggers.length} ${triggers.length === 1 ? "trigger" : "triggers"}`
      : "manual",
    requiredTools.length > 0
      ? `${requiredTools.length} ${requiredTools.length === 1 ? "tool" : "tools"}`
      : undefined,
  ]
    .filter(Boolean)
    .join(" · ");
  return (
    <section
      className="session-skills-focus"
      data-testid="session-skills-focus"
      aria-label={`Skill review ${skill.name}`}
    >
      <div className="session-skills-focus-head">
        <span>
          {origin} · {skillSizeLabel(skill)}
        </span>
        <strong>{skill.name}</strong>
        <small>{skill.description || "No summary"}</small>
      </div>
      {(onLoadBody && !body) || onEdit || onClone || onAskDelete ? (
        <div className="session-skill-actions">
          {onLoadBody && !body ? (
            <button type="button" className="node-action" onClick={onLoadBody}>
              Load full content
            </button>
          ) : null}
          {onEdit ? (
            <button type="button" className="node-action" onClick={onEdit}>
              Edit skill
            </button>
          ) : null}
          {onClone ? (
            <button type="button" className="node-action" onClick={onClone}>
              Clone skill
            </button>
          ) : null}
          {onAskDelete ? (
            deleteConfirmName === skill.name ? (
              <span
                className="session-skill-delete-confirm"
                role="group"
                aria-label={`Confirm delete ${skill.name}`}
              >
                <button
                  type="button"
                  className="node-action"
                  disabled={deletingSkillName === skill.name}
                  onClick={onCancelDelete}
                >
                  Cancel
                </button>
                <button
                  type="button"
                  className="node-action danger-action"
                  disabled={!!deletingSkillName}
                  onClick={onConfirmDelete}
                >
                  {deletingSkillName === skill.name
                    ? "Deleting"
                    : "Confirm delete"}
                </button>
              </span>
            ) : (
              <button
                type="button"
                className="node-action danger-action"
                disabled={!!deletingSkillName}
                onClick={onAskDelete}
              >
                Delete
              </button>
            )
          ) : null}
        </div>
      ) : null}
      {reviewIssues.length > 0 ? (
        <SkillReviewIssues issues={reviewIssues} />
      ) : null}
      <section
        className="session-skills-focus-rules"
        aria-label="Skill activation rules"
      >
        <div className="session-skills-focus-rules-head">
          <strong>Activation</strong>
          <span>{activationSummaryText}</span>
        </div>
        <div className="session-skills-focus-chips">
          {triggers.length > 0 ? (
            triggers
              .slice(0, 10)
              .map((trigger) => (
                <span key={`trigger:${trigger}`}>{trigger}</span>
              ))
          ) : (
            <span data-kind="muted">Manual only</span>
          )}
          {triggers.length > 10 ? (
            <span data-kind="muted">+{triggers.length - 10} triggers</span>
          ) : null}
          {requiredTools.length > 0 ? (
            requiredTools.map((tool) => (
              <span key={`tool:${tool}`} data-kind="tool">
                {tool}
              </span>
            ))
          ) : (
            <span data-kind="muted">No required tools</span>
          )}
        </div>
      </section>
      <details className="session-skills-focus-disclosure session-skills-focus-content">
        <summary>
          <strong>SKILL.md</strong>
          <span>
            {body
              ? "full content loaded"
              : onLoadBody
                ? "load on demand"
                : preview
                  ? "preview"
                  : "unavailable"}
          </span>
        </summary>
        <div className="session-skills-focus-body">
          {bodyState?.loading ? <p>Loading full content...</p> : null}
          {bodyState?.error ? <p className="error">{bodyState.error}</p> : null}
          {!bodyState?.loading && !bodyState?.error && body ? (
            <>
              <CopyButton label="Copy" value={body} className="node-action" />
              <pre className="session-skill-body">{body}</pre>
            </>
          ) : null}
          {!bodyState?.loading && !bodyState?.error && !body && preview ? (
            <p>{summarizeSkillPreview(preview)}</p>
          ) : null}
        </div>
      </details>
    </section>
  );
}

function SkillReviewIssues({
  issues,
}: {
  issues: ReturnType<typeof skillReviewIssues>;
}) {
  return (
    <section
      className="session-skills-focus-review"
      data-testid="session-skills-focus-review"
      aria-label="Skill review issues"
    >
      <span>Review</span>
      <ul>
        {issues.map((issue) => (
          <li key={issue.kind}>
            <strong>{issue.label}</strong>
            <span>{issue.detail}</span>
          </li>
        ))}
      </ul>
    </section>
  );
}

function skillReviewIssues(
  skill: SessionSkillInfo,
): Array<{ kind: SkillReviewKind; label: string; detail: string }> {
  if (!skill.runtime) return [];
  const issues: Array<{
    kind: SkillReviewKind;
    label: string;
    detail: string;
  }> = [];
  if (skillTriggers(skill).length === 0) {
    issues.push({
      kind: "manual",
      label: "Manual only",
      detail: "Add triggers so Affent can select this workflow automatically.",
    });
  }
  if (!skill.description?.trim()) {
    issues.push({
      kind: "summary",
      label: "Missing summary",
      detail: "Add a short summary so humans can scan the workflow list.",
    });
  }
  return issues;
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
  return normalized.length > 180
    ? `${normalized.slice(0, 179).trimEnd()}...`
    : normalized;
}

type SkillFormState = {
  name: string;
  description: string;
  triggers: string;
  requiredTools: string;
  body: string;
};

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
            <button
              type="button"
              className="ghost-action"
              onClick={onCancelEdit}
            >
              Cancel edit
            </button>
          ) : null}
        </div>
      ) : null}
      <label>
        <span>Name</span>
        <input
          value={form.name}
          onChange={(event) =>
            setForm((current) => ({ ...current, name: event.target.value }))
          }
          placeholder="my_skill"
          required
          disabled={!!editingSkillName}
        />
      </label>
      <label>
        <span>Summary</span>
        <input
          value={form.description}
          onChange={(event) =>
            setForm((current) => ({
              ...current,
              description: event.target.value,
            }))
          }
          placeholder="When this skill should be used"
        />
      </label>
      <label>
        <span>Triggers</span>
        <input
          value={form.triggers}
          onChange={(event) =>
            setForm((current) => ({ ...current, triggers: event.target.value }))
          }
          placeholder="comma or newline separated"
        />
      </label>
      <label>
        <span>Required tools</span>
        <input
          value={form.requiredTools}
          onChange={(event) =>
            setForm((current) => ({
              ...current,
              requiredTools: event.target.value,
            }))
          }
          placeholder="workspace, browser, web"
        />
      </label>
      <div className="session-skill-form-body">
        <div className="session-skill-editor-head">
          <span>Full content</span>
          <code>{skillFormPath(form.name)}</code>
          <small>{skillBodyStats(form.body)}</small>
        </div>
        <textarea
          aria-label="Full content"
          value={form.body}
          onChange={(event) =>
            setForm((current) => ({ ...current, body: event.target.value }))
          }
          placeholder="AFFENT ACTIVE SKILL: my_skill&#10;Use this workflow..."
          required
        />
      </div>
      {installError ? (
        <div className="session-skills-empty error">{installError}</div>
      ) : null}
      <button
        type="submit"
        className="session-skills-add-submit"
        disabled={installing || !form.name.trim() || !form.body.trim()}
      >
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
    splitList(form.triggers)?.length
      ? `Triggers: ${splitList(form.triggers)?.join(", ")}`
      : undefined,
    splitList(form.requiredTools)?.length
      ? `Required tools: ${splitList(form.requiredTools)?.join(", ")}`
      : undefined,
    "",
    "Content:",
    form.body.trim(),
  ]
    .filter((line): line is string => line != null)
    .join("\n");
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

function skillFormPath(name: string): string {
  const clean = name.trim() || "new_skill";
  return `${clean}/SKILL.md`;
}

function skillBodyStats(body: string): string {
  const lineCount = body.length > 0 ? body.split("\n").length : 0;
  return `${lineCount} ${lineCount === 1 ? "line" : "lines"} · ${body.length} chars`;
}

function clonedSkillName(
  name: string,
  skills: readonly SessionSkillInfo[],
): string {
  const existing = new Set(skills.map((skill) => skill.name));
  const base =
    `${name}_copy`.replace(/[^a-zA-Z0-9_-]+/g, "_").replace(/^_+|_+$/g, "") ||
    "skill_copy";
  if (!existing.has(base)) return base;
  for (let index = 2; index < 100; index += 1) {
    const candidate = `${base}_${index}`;
    if (!existing.has(candidate)) return candidate;
  }
  return `${base}_${Date.now()}`;
}

function emptySkillsText(canInstall: boolean): string {
  return canInstall
    ? "No reusable workflows saved yet."
    : "No skills returned by this runtime.";
}

function formatPanelError(err: unknown): string {
  if (err instanceof Error) return err.message;
  return "Unknown error";
}
