import { useEffect, useLayoutEffect, useRef, useState, type ChangeEvent, type DragEvent, type KeyboardEvent } from "react";
import { buildComposerTaskHint } from "../view/composerTaskHint";
import { draftMergeMode, draftSourceLabel, type DraftSource } from "../view/draftSource";
import type { RuntimeCapabilityView } from "../view/runtimeCapabilities";

export interface ComposerDraft {
  id: number;
  content: string;
  source?: DraftSource;
}

interface DraftContext {
  label: string;
  content: string;
  mode: "append" | "replace";
  preview: string;
  source?: DraftSource;
  action?: "loop_setup";
}

export function Composer({
  disabled,
  busy,
  cancelling = false,
  hasSession = true,
  draft,
  focusSignal,
  disabledReason,
  runtimeCapabilities,
  onSubmit,
  onStartLoop,
  onCancel,
}: {
  disabled: boolean;
  busy: boolean;
  cancelling?: boolean;
  hasSession?: boolean;
  resumeSession?: boolean;
  draft?: ComposerDraft;
  focusSignal?: number;
  disabledReason?: string;
  runtimeCapabilities?: RuntimeCapabilityView;
  onSubmit: (content: string) => Promise<void>;
  onStartLoop?: (goal: string) => Promise<void>;
  onScheduleLoopTick?: () => Promise<void> | void;
  onScheduleCheckIn?: () => Promise<void> | void;
  onScheduleDaily?: () => Promise<void> | void;
  automationAvailable?: boolean;
  automationBusy?: "loop" | "checkin" | "daily";
  onCancel: () => Promise<void>;
}) {
  const [content, setContent] = useState("");
  const [dragActive, setDragActive] = useState(false);
  const [draftContext, setDraftContext] = useState<DraftContext | undefined>();
  const [dismissedDraftId, setDismissedDraftId] = useState<number | undefined>();
  const textareaRef = useRef<HTMLTextAreaElement | null>(null);
  const addMenuRef = useRef<HTMLDetailsElement | null>(null);
  const fileInputRef = useRef<HTMLInputElement | null>(null);

  useLayoutEffect(() => {
    const textarea = textareaRef.current;
    if (!textarea) return;
    textarea.style.height = "auto";
    textarea.style.height = `${Math.min(textarea.scrollHeight, 180)}px`;
  }, [content]);

  useEffect(() => {
    if (!draft || disabled) return;
    if (draft.id === dismissedDraftId) return;
    const label = draftSourceLabel(draft.source);
    const incoming = draft.content.trim();
    if (!incoming || !label) return;
    const replace = draftMergeMode(draft.source) === "replace" || !!draftContext;
    setContent((current) => {
      const merged = mergeDraftContent(current, incoming, replace);
      setDraftContext({
        label,
        content: incoming,
        mode: merged.mode,
        preview: summarizeDraft(incoming, 92),
        source: draft.source,
        action: draft.source === "loop_setup" ? "loop_setup" : undefined,
      });
      return merged.content;
    });
    textareaRef.current?.focus();
  }, [disabled, dismissedDraftId, draft]);

  useEffect(() => {
    if (!focusSignal || disabled) return;
    textareaRef.current?.focus();
  }, [disabled, focusSignal]);

  useEffect(() => {
    function handleDocumentPointerDown(event: PointerEvent) {
      const menu = addMenuRef.current;
      if (!menu?.open) return;
      const target = event.target;
      if (target instanceof Node && menu.contains(target)) return;
      menu.open = false;
    }

    document.addEventListener("pointerdown", handleDocumentPointerDown);
    return () => document.removeEventListener("pointerdown", handleDocumentPointerDown);
  }, []);

  async function submit() {
    const trimmed = content.trim();
    if (!trimmed || disabled || cancelling) return;
    try {
      if (draftContext?.action === "loop_setup" && onStartLoop) {
        await onStartLoop(trimmed);
      } else {
        await onSubmit(trimmed);
      }
      setContent("");
      setDraftContext(undefined);
    } catch {
      textareaRef.current?.focus();
    }
  }

  function handleKeyDown(event: KeyboardEvent<HTMLTextAreaElement>) {
    if (event.key === "Escape" && content.trim() !== "" && !disabled && !busy) {
      event.preventDefault();
      setContent("");
      setDraftContext(undefined);
      return;
    }
    if (event.key !== "Enter" || event.shiftKey || event.nativeEvent.isComposing) return;
    event.preventDefault();
    void submit();
  }

  function handleChange(event: ChangeEvent<HTMLTextAreaElement>) {
    setContent(event.target.value);
  }

  function clearContent() {
    setContent("");
    setDraftContext(undefined);
    textareaRef.current?.focus();
  }

  function removeDraftContext() {
    if (!draftContext) return;
    setDismissedDraftId(draft?.id);
    setContent((current) => draftContext.mode === "append" ? removeAppendedDraft(current, draftContext.content) : "");
    setDraftContext(undefined);
    textareaRef.current?.focus();
  }

  function canAcceptDrop() {
    return !disabled && !busy;
  }

  function appendContent(next: string) {
    const incoming = next.trim();
    if (!incoming) return;
    setContent((current) => {
      const trimmedCurrent = current.trimEnd();
      return trimmedCurrent ? `${trimmedCurrent}\n${incoming}` : incoming;
    });
  }

  function closeAddMenu() {
    if (addMenuRef.current) addMenuRef.current.open = false;
  }

  function insertTemplate(template: string) {
    appendContent(template);
    closeAddMenu();
    requestAnimationFrame(() => textareaRef.current?.focus());
  }

  function startLoopDraft() {
    setDraftContext({
      label: "Loop setup",
      content: "",
      mode: "replace",
      preview: "Next message will start loop setup.",
      source: "starter",
      action: "loop_setup",
    });
    closeAddMenu();
    requestAnimationFrame(() => textareaRef.current?.focus());
  }

  function openFilePicker() {
    closeAddMenu();
    fileInputRef.current?.click();
  }

  async function handleUploadFiles(event: ChangeEvent<HTMLInputElement>) {
    const files = Array.from(event.target.files ?? []);
    event.target.value = "";
    if (files.length === 0) return;
    const snippets = await Promise.all(files.map(fileDraft));
    appendContent(snippets.join("\n\n"));
    textareaRef.current?.focus();
  }

  function handleDragEnter(event: DragEvent<HTMLDivElement>) {
    if (!canAcceptDrop()) return;
    event.preventDefault();
    setDragActive(true);
  }

  function handleDragOver(event: DragEvent<HTMLDivElement>) {
    if (!canAcceptDrop()) return;
    event.preventDefault();
    event.dataTransfer.dropEffect = "copy";
    setDragActive(true);
  }

  function handleDragLeave(event: DragEvent<HTMLDivElement>) {
    const nextTarget = event.relatedTarget;
    if (nextTarget instanceof Node && event.currentTarget.contains(nextTarget)) return;
    setDragActive(false);
  }

  function handleDrop(event: DragEvent<HTMLDivElement>) {
    if (!canAcceptDrop()) return;
    event.preventDefault();
    setDragActive(false);
    const droppedText = event.dataTransfer.getData("text/plain");
    const droppedFiles = Array.from(event.dataTransfer.files)
      .map((file) => file.name)
      .filter(Boolean)
      .join("\n");
    appendContent(droppedText || droppedFiles);
    textareaRef.current?.focus();
  }

  const active = busy || cancelling || dragActive || content.trim() !== "";
  const contentText = content.trim();
  const hasContent = contentText !== "";
  const lineCount = hasContent ? contentText.split(/\r\n|\r|\n/).length : 0;
  const composerStatus = composerStatusLabel({ busy, cancelling, dragActive, draftContext, hasSession, hasContent });
  const composerMeta = composerMetaLabel({ contentText, lineCount, draftContext, busy, cancelling });
  const taskHint = buildComposerTaskHint(contentText, runtimeCapabilities);
  const showIntent = composerStatus !== "" || !!composerMeta;
  const placeholder = "Message Affent...";

  if (disabled) {
    return (
      <div className="composer composer-readonly-shell" data-active="false" data-readonly="true" data-testid="composer">
        <div className="composer-readonly" role="status">
          <strong>Preview replay</strong>
          <span>{disabledReason || "Connect affentserve to send messages."}</span>
        </div>
      </div>
    );
  }

  return (
    <div
      className="composer"
      data-active={active}
      data-busy={busy ? "true" : "false"}
      data-cancelling={cancelling ? "true" : "false"}
      data-dragging={dragActive}
      data-resume-idle="false"
      data-readonly="false"
      data-testid="composer"
      onDragEnter={handleDragEnter}
      onDragOver={handleDragOver}
      onDragLeave={handleDragLeave}
      onDrop={handleDrop}
    >
      {dragActive ? (
        <div className="composer-drop-hint" role="status">
          Drop into message
        </div>
      ) : null}
      {showIntent ? (
        <div className="composer-intent" aria-live="polite" data-testid="composer-intent">
          {composerStatus ? <span>{composerStatus}</span> : null}
          {composerMeta ? <small>{composerMeta}</small> : null}
        </div>
      ) : null}
      {draftContext ? (
        <div className="composer-context" data-testid="composer-context">
          {draftContext.mode === "append" ? <span className="composer-context-mode">{draftModeLabel(draftContext.mode)}</span> : null}
          <div className="composer-context-copy">
            <span>{draftContext.label}</span>
            <small title={draftContext.content}>{draftContext.preview}</small>
          </div>
          <button type="button" onClick={removeDraftContext}>
            Remove
          </button>
        </div>
      ) : null}
      {taskHint ? (
        <div className="composer-task-hint" data-tone={taskHint.tone} data-testid="composer-task-hint">
          <span>{taskHint.label}</span>
          <small>{taskHint.detail}</small>
        </div>
      ) : null}
      <div className="composer-input-row">
        <details className="composer-add" ref={addMenuRef} data-testid="composer-add">
          <summary className="composer-add-trigger" role="button" aria-label="Add context or automation">
            +
          </summary>
          <div className="composer-add-menu">
            <button type="button" onClick={openFilePicker}>
              Upload file
            </button>
            <button type="button" onClick={startLoopDraft}>
              Loop
            </button>
            <button type="button" onClick={() => insertTemplate(schedulePromptTemplate)}>
              Scheduled task
            </button>
          </div>
        </details>
        <input
          ref={fileInputRef}
          className="composer-file-input"
          type="file"
          tabIndex={-1}
          multiple
          onChange={(event) => {
            void handleUploadFiles(event);
          }}
        />
        <textarea
          ref={textareaRef}
          value={content}
          onChange={handleChange}
          onKeyDown={handleKeyDown}
          placeholder={placeholder}
          rows={1}
        />
      </div>
      <div className="composer-actions">
        {content.trim() !== "" && !draftContext ? (
          <button type="button" className="ghost-action" onClick={clearContent}>
            Clear
          </button>
        ) : null}
        {busy ? (
          <button type="button" className="secondary-action" disabled={cancelling} onClick={() => void onCancel()}>
            {cancelling ? "Stopping" : "Stop"}
          </button>
        ) : null}
      </div>
    </div>
  );
}

function mergeDraftContent(current: string, incoming: string, replace: boolean): { content: string; mode: DraftContext["mode"] } {
  const next = incoming.trim();
  if (!next) return { content: current, mode: replace ? "replace" : "append" };
  if (replace) return { content: next, mode: "replace" };
  if (current.trim() === "") return { content: next, mode: "append" };
  if (current.trim() === next) return { content: current, mode: "replace" };
  return { content: `${current.trimEnd()}\n\n${next}`, mode: "append" };
}

function removeAppendedDraft(current: string, draftContent: string): string {
  const trimmed = current.trimEnd();
  const suffix = `\n\n${draftContent}`;
  if (trimmed.endsWith(suffix)) return trimmed.slice(0, -suffix.length);
  if (trimmed === draftContent) return "";
  return current;
}

function summarizeDraft(text: string, limit: number): string {
  const singleLine = text.replace(/\s+/g, " ").trim();
  if (singleLine.length <= limit) return singleLine;
  return `${singleLine.slice(0, Math.max(0, limit - 3)).trimEnd()}...`;
}

function draftModeLabel(mode: DraftContext["mode"]): string {
  return mode === "append" ? "Added" : "";
}

const schedulePromptTemplate = "Every day at UTC+8 09:00, ";

async function fileDraft(file: File): Promise<string> {
  if (!isReadableText(file)) {
    return [`Attached file: ${file.name}`, `Type: ${file.type || "unknown"}`, `Size: ${formatFileSize(file.size)}`].join("\n");
  }
  const cap = 24000;
  const text = await readFileText(file);
  const body = text.length > cap ? `${text.slice(0, cap)}\n\n[File truncated: ${text.length - cap} characters omitted]` : text;
  return [`Attached file: ${file.name}`, `Size: ${formatFileSize(file.size)}`, "", body].join("\n");
}

function readFileText(file: File): Promise<string> {
  const maybeText = (file as File & { text?: () => Promise<string> }).text;
  if (typeof maybeText === "function") {
    return maybeText.call(file);
  }
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(typeof reader.result === "string" ? reader.result : "");
    reader.onerror = () => reject(reader.error ?? new Error("read file failed"));
    reader.readAsText(file);
  });
}

function isReadableText(file: File): boolean {
  if (file.type.startsWith("text/")) return true;
  return /\.(md|mdx|txt|json|jsonl|yaml|yml|toml|ini|env|csv|ts|tsx|js|jsx|mjs|cjs|css|scss|html|xml|go|py|rs|java|kt|swift|c|cc|cpp|h|hpp|sh|bash|zsh|sql|graphql|proto|diff|patch)$/i.test(file.name);
}

function formatFileSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  const kib = bytes / 1024;
  if (kib < 1024) return `${kib.toFixed(kib >= 10 ? 0 : 1)} KiB`;
  const mib = kib / 1024;
  return `${mib.toFixed(mib >= 10 ? 0 : 1)} MiB`;
}

function composerStatusLabel({
  busy,
  cancelling,
  dragActive,
  draftContext,
  hasSession,
  hasContent,
}: {
  busy: boolean;
  cancelling: boolean;
  dragActive: boolean;
  draftContext?: DraftContext;
  hasSession: boolean;
  hasContent: boolean;
}): string {
  if (dragActive) return "Adding context";
  if (cancelling) return "Stopping run";
  if (busy) return hasContent ? "Guidance ready" : "Live run";
  if (draftContext) {
    if (draftContext.action === "loop_setup") return "Loop setup ready";
    if (draftContext.source === "retry_reply") return "Retry ready";
    return draftContext.mode === "append" ? "Follow-up with context" : "Draft ready";
  }
  if (!hasSession) return hasContent ? "Ready to start" : "";
  return "";
}

function composerMetaLabel({
  contentText,
  lineCount,
  draftContext,
  busy,
  cancelling,
}: {
  contentText: string;
  lineCount: number;
  draftContext?: DraftContext;
  busy: boolean;
  cancelling: boolean;
}): string | undefined {
  if (cancelling) return "Waiting for Affent to stop safely";
  if (busy) return contentText ? "Sends into this run, not a new chat" : "Type guidance while Affent works";
  if (draftContext) return draftContext.label;
  if (!contentText) return undefined;
  const charCount = contentText.length;
  return `${lineCount} ${lineCount === 1 ? "line" : "lines"} · ${charCount} chars`;
}
