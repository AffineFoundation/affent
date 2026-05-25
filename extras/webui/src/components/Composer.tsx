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
}

export function Composer({
  disabled,
  busy,
  cancelling = false,
  hasSession = true,
  resumeSession = false,
  draft,
  focusSignal,
  disabledReason,
  runtimeCapabilities,
  onSubmit,
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
  onCancel: () => Promise<void>;
}) {
  const [content, setContent] = useState("");
  const [dragActive, setDragActive] = useState(false);
  const [draftContext, setDraftContext] = useState<DraftContext | undefined>();
  const [dismissedDraftId, setDismissedDraftId] = useState<number | undefined>();
  const textareaRef = useRef<HTMLTextAreaElement | null>(null);

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
      setDraftContext({ label, content: incoming, mode: merged.mode, preview: summarizeDraft(incoming, 92), source: draft.source });
      return merged.content;
    });
    textareaRef.current?.focus();
  }, [disabled, dismissedDraftId, draft]);

  useEffect(() => {
    if (!focusSignal || disabled) return;
    textareaRef.current?.focus();
  }, [disabled, focusSignal]);

  async function submit() {
    const trimmed = content.trim();
    if (!trimmed || disabled || cancelling) return;
    try {
      await onSubmit(trimmed);
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
  const composerStatus = composerStatusLabel({ busy, cancelling, dragActive, draftContext, hasSession, resumeSession, hasContent });
  const composerMeta = composerMetaLabel({ contentText, lineCount, draftContext, busy, cancelling, hasSession, resumeSession });
  const taskHint = buildComposerTaskHint(contentText, runtimeCapabilities);
  const compactResume = resumeSession && !busy && !hasContent && !draftContext && !taskHint;
  const placeholder = "Message Affent...";
  const primaryLabel = primaryActionLabel({
    busy,
    hasSession,
    resumeSession,
    draftContext,
    taskHintActive: Boolean(taskHint),
  });

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
      data-resume-idle={compactResume ? "true" : "false"}
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
      <div className="composer-intent" aria-live="polite" data-testid="composer-intent">
        <span>{composerStatus}</span>
        {composerMeta ? <small>{composerMeta}</small> : null}
      </div>
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
      <textarea
        ref={textareaRef}
        value={content}
        onChange={handleChange}
        onKeyDown={handleKeyDown}
        placeholder={placeholder}
        rows={1}
      />
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
        <button type="button" className="primary-action" disabled={content.trim() === "" || cancelling} onClick={() => void submit()}>
          {primaryLabel}
        </button>
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

function composerStatusLabel({
  busy,
  cancelling,
  dragActive,
  draftContext,
  hasSession,
  resumeSession,
  hasContent,
}: {
  busy: boolean;
  cancelling: boolean;
  dragActive: boolean;
  draftContext?: DraftContext;
  hasSession: boolean;
  resumeSession: boolean;
  hasContent: boolean;
}): string {
  if (dragActive) return "Adding context";
  if (cancelling) return "Stopping run";
  if (busy) return hasContent ? "Guidance ready" : "Live run";
  if (draftContext) {
    if (draftContext.source === "retry_reply") return "Retry ready";
    return draftContext.mode === "append" ? "Follow-up with context" : "Draft ready";
  }
  if (!hasSession) return hasContent ? "Ready to start" : "New task";
  if (resumeSession) return hasContent ? "Ready to resume" : "Resume chat";
  return hasContent ? "Ready to send" : "Follow-up";
}

function primaryActionLabel({
  busy,
  hasSession,
  resumeSession,
  draftContext,
  taskHintActive,
}: {
  busy: boolean;
  hasSession: boolean;
  resumeSession: boolean;
  draftContext?: DraftContext;
  taskHintActive: boolean;
}): string {
  if (busy) return "Send guidance";
  if (taskHintActive && !draftContext) {
    if (resumeSession) return "Resume anyway";
    return hasSession ? "Send anyway" : "Start anyway";
  }
  if (!hasSession) return "Start";
  if (resumeSession && !draftContext) return "Resume";
  if (draftContext?.source === "retry_reply") return "Retry";
  if (draftContext?.source === "previous_message") return "Send edited";
  if (draftContext) return "Send follow-up";
  return "Send";
}

function composerMetaLabel({
  contentText,
  lineCount,
  draftContext,
  busy,
  cancelling,
  hasSession,
  resumeSession,
}: {
  contentText: string;
  lineCount: number;
  draftContext?: DraftContext;
  busy: boolean;
  cancelling: boolean;
  hasSession: boolean;
  resumeSession: boolean;
}): string | undefined {
  if (cancelling) return "Waiting for Affent to stop safely";
  if (busy) return contentText ? "Sends into this run, not a new chat" : "Type guidance while Affent works";
  if (draftContext) return draftContext.label;
  if (!contentText) {
    if (resumeSession) return "Type a message to continue this chat";
    return hasSession ? "Continue the conversation" : undefined;
  }
  const charCount = contentText.length;
  return `${lineCount} ${lineCount === 1 ? "line" : "lines"} · ${charCount} chars`;
}
