import { useEffect, useRef, useState, type CSSProperties } from "react";
import { EventType } from "../api/events";
import type { NormalizedEvent } from "../normalize/normalizeEvent";
import type { ToolCallState, TurnError, TurnState } from "../store/sessionState";
import type { UseAsDraft } from "../view/draftSource";
import { summarizeUserError } from "../view/errorSummary";
import { buildExecutionTree, searchableExecutionNodeText } from "../view/executionTree";
import { buildTurnActivity, type TurnActivityBriefRow, type TurnActivityEvidence, type TurnActivityNode, type TurnActivityView } from "../view/turnActivity";
import { buildTurnBoundaryView } from "../view/turnBoundary";
import { buildTurnWorkSummaryWithOptions, type TurnWorkSummary, type WorkSummaryItem } from "../view/turnWorkSummary";
import { buildTurnArtifacts, type TurnArtifact } from "../view/turnArtifacts";
import { CopyButton } from "./CopyButton";
import { CopyMenu } from "./CopyMenu";
import { ExecutionTree } from "./ExecutionTree";
import { HighlightText } from "./HighlightText";
import { MarkdownText } from "./MarkdownText";
import { markdownToPlainText } from "../view/textPreview";

// One turn as a flowing work segment. Human-readable actions stay in the
// timeline; structured debug data is available inline through disclosure
// controls instead of a separate global mode.
export function TurnCard({
  turn,
  turnNumber,
  anchorId,
  events,
  searchQuery,
  sessionId,
  isLatest = true,
  showHeader = false,
  showBoundary = true,
  forceWorkDetails = false,
  onOpenArtifact,
  onUseAsDraft,
}: {
  turn: TurnState;
  turnNumber: number;
  anchorId?: string;
  events: readonly NormalizedEvent[];
  searchQuery?: string;
  sessionId?: string;
  isLatest?: boolean;
  showHeader?: boolean;
  showBoundary?: boolean;
  forceWorkDetails?: boolean;
  onOpenArtifact?: (path: string) => void;
  onUseAsDraft?: UseAsDraft;
}) {
  const relatedEvents = events.filter((ev) => eventBelongsToTurn(ev, turn));
  const title = turnTitle(turn);
  const continuedAfterLimit = !isLatest && turn.status === "max_turns";
  const continuedIntoTurnNumber = continuedAfterLimit ? turnNumber + 1 : undefined;
  const workSummary = buildTurnWorkSummaryWithOptions(turn, { continuedAfterLimit });
  const artifacts = buildTurnArtifacts(turn);
  const activity = buildTurnActivity(turn, { continuedAfterLimit, continuedIntoTurnNumber });
  const fallbackAnswer = buildFallbackAnswer(turn, { continuedAfterLimit });
  const boundary = buildTurnBoundaryView({ turn, turnNumber, artifactCount: artifacts.length, continuedAfterLimit });
  const headerMeta = boundary.meta.filter((item) => item !== workSummary.actionLabel);
  const workSearchMatch = workSearchMatches(turn, relatedEvents, searchQuery);
  const showWorkDetails = shouldShowWorkDetails(turn, {
    isLatest,
    continuedAfterLimit,
    searchMatch: workSearchMatch,
    force: forceWorkDetails,
  });
  const activityShowsReasoning = activity?.items.some((item) => item.kind === "reasoning") ?? false;
  const showReasoningDisclosure = shouldShowReasoningDisclosure(turn, {
    activityShowsReasoning,
    isLatest,
    searchQuery,
  });

  return (
    <section id={anchorId} className="flow-turn" data-testid="turn-card" data-status={turn.status}>
      <header className="flow-turn-head" data-testid="turn-head" data-visible={showHeader ? "true" : "false"}>
        <div className="turn-title-group">
          <div className="turn-index">Message {turnNumber}</div>
          <div className="turn-title" data-testid="turn-title" title={title}>
            <HighlightText text={title} query={searchQuery} />
          </div>
        </div>
        <div className="flow-status">
          <span className="pulse-dot" data-status={turn.status} aria-hidden="true" />
          <span>{humanTurnStatus(turn.status, turn.endReason, { continuedAfterLimit })}</span>
        </div>
        <div className="flow-metrics">
          {turn.toolCalls.length > 0 ? <span>{workSummary.actionLabel}</span> : null}
          {headerMeta.map((item) => (
            <span key={item}>{item}</span>
          ))}
          {workSummary.headlineItems.map((item) => (
            <span key={`${item.tone}-${item.label}`} className="metric-chip" data-tone={item.tone}>
              {item.label}
            </span>
          ))}
        </div>
      </header>
      {showBoundary ? (
        <div
          className="turn-boundary"
          data-testid="turn-boundary"
          aria-label={boundary.ariaLabel}
          data-status={turn.status}
          data-tone={boundary.tone}
        >
          <span className="turn-boundary-index">{`Message ${turnNumber}`}</span>
          <span className="turn-boundary-title" title={title}>
            <HighlightText text={boundary.title} query={searchQuery} />
          </span>
          <small className="turn-boundary-state">{boundary.statusLabel}</small>
          {boundary.meta.map((item) => (
            <small key={item} className="turn-boundary-meta">
              {item}
            </small>
          ))}
        </div>
      ) : null}
      <div className="conversation-turn">
        {turn.userText ? (
          <MessageStep
            label="You"
            text={turn.userText}
            variant="user"
            searchQuery={searchQuery}
            onReuse={onUseAsDraft}
          />
        ) : null}
        <div className="assistant-cluster">
          <div className="assistant-name">Affent</div>
          {turn.assistantText ? (
            <MessageStep
              label="Affent"
              text={turn.assistantText}
              variant="assistant"
              streaming={turn.messageStreaming}
              searchQuery={searchQuery}
              onContinue={onUseAsDraft}
            />
          ) : null}
          {turn.status === "running" && !turn.assistantText ? (
            <RunningAnswerBubble turn={turn} summary={workSummary} />
          ) : null}
          {activity ? <AgentActivity activity={activity} isLatest={isLatest} searchQuery={searchQuery} onUseAsDraft={onUseAsDraft} /> : null}
          {fallbackAnswer ? (
            <FallbackAnswerBubble answer={fallbackAnswer} searchQuery={searchQuery} onUseAsDraft={onUseAsDraft} />
          ) : null}
          {artifacts.length > 0 ? (
            <ArtifactStrip
              artifacts={artifacts}
              sessionId={sessionId}
              onOpenArtifact={onOpenArtifact}
              onUseAsDraft={onUseAsDraft}
              searchQuery={searchQuery}
            />
          ) : null}
          {showReasoningDisclosure ? (
            <ReasoningDisclosure turn={turn} searchQuery={searchQuery} />
          ) : null}
          {showWorkDetails ? (
            <WorkDetails
              turn={turn}
              summary={workSummary}
              events={relatedEvents}
              searchQuery={searchQuery}
              searchMatch={workSearchMatch}
              sessionId={sessionId}
              onOpenArtifact={onOpenArtifact}
              onUseAsDraft={onUseAsDraft}
              continuedAfterLimit={continuedAfterLimit}
              continuedIntoTurnNumber={continuedIntoTurnNumber}
            />
          ) : null}
          {turn.status === "max_turns" && !continuedAfterLimit ? <ContinuationPrompt turn={turn} onUseAsDraft={onUseAsDraft} /> : null}
          {turn.error ? <ErrorBlock error={turn.error} onUseAsDraft={onUseAsDraft} /> : null}
        </div>
      </div>
    </section>
  );
}

function shouldShowReasoningDisclosure(
  turn: TurnState,
  opts: { activityShowsReasoning: boolean; isLatest: boolean; searchQuery?: string },
): boolean {
  const thinking = turn.thinkingText.trim();
  if (!thinking || opts.activityShowsReasoning) return false;
  if (turn.thinkingStreaming || turn.status === "running") return true;
  const query = opts.searchQuery?.trim().toLowerCase();
  if (query && thinking.toLowerCase().includes(query)) return true;
  return opts.isLatest;
}

function shouldShowWorkDetails(
  turn: TurnState,
  opts: { isLatest: boolean; continuedAfterLimit?: boolean; searchMatch?: boolean; force?: boolean },
): boolean {
  if (turn.toolCalls.length === 0) return false;
  if (opts.searchMatch) return true;
  if (opts.force) return true;
  if (turn.status === "running") return false;
  if (turn.status === "error" || turn.error) return true;
  if (latestFailedTool(turn) && !turn.assistantText.trim()) return true;
  if (opts.continuedAfterLimit) return false;
  return opts.isLatest;
}

function ReasoningDisclosure({ turn, searchQuery }: { turn: TurnState; searchQuery?: string }) {
  const autoOpen = turn.thinkingStreaming;
  const userTouched = useRef(false);
  const [open, setOpen] = useState(autoOpen);

  useEffect(() => {
    if (!userTouched.current) setOpen(autoOpen);
  }, [autoOpen]);

  function toggleOpen() {
    userTouched.current = true;
    setOpen((current) => !current);
  }

  return (
    <section className="inline-disclosure thinking-disclosure secondary-disclosure" data-open={open ? "true" : "false"}>
      <button type="button" className="disclosure-button" aria-expanded={open} onClick={toggleOpen}>
        <span>Thinking</span>
        {turn.thinkingStreaming ? <span className="live-chip">live</span> : null}
        <span className="disclosure-chevron" aria-hidden="true" />
      </button>
      {open ? (
        <div className={`flow-text muted${turn.thinkingStreaming ? " streaming-caret" : ""}`}>
          <HighlightText text={turn.thinkingText} query={searchQuery} />
        </div>
      ) : null}
    </section>
  );
}

function AgentActivity({
  activity,
  isLatest,
  searchQuery,
  onUseAsDraft,
}: {
  activity: TurnActivityView;
  isLatest: boolean;
  searchQuery?: string;
  onUseAsDraft?: UseAsDraft;
}) {
  const [openOverrides, setOpenOverrides] = useState<Record<string, boolean>>({});
  const autoOpen = activity.live || (isLatest && activity.tone === "error") || Boolean(searchQuery?.trim());
  const userTouched = useRef(false);
  const [open, setOpen] = useState(autoOpen);
  const showDigestLabel = activity.digest.label !== activity.statusLabel;
  const issueNodes = activityIssueNodes(activity.nodes);

  useEffect(() => {
    if (!userTouched.current) setOpen(autoOpen);
  }, [autoOpen]);

  function setNodeOpen(nodeId: string, open: boolean) {
    setOpenOverrides((current) => ({ ...current, [nodeId]: open }));
  }

  function toggleOpen() {
    userTouched.current = true;
    setOpen((current) => !current);
  }

  const digestLabel = agentActivityDigestLabel(activity, showDigestLabel);

  return (
    <section
      className="agent-activity"
      data-testid="agent-activity"
      data-live={activity.live ? "true" : "false"}
      data-tone={activity.tone}
      data-open={open ? "true" : "false"}
      aria-label={activity.title}
    >
      <div className="agent-activity-headbar">
        <button type="button" className="agent-activity-head" aria-expanded={open} onClick={toggleOpen}>
          <span>{activity.title}</span>
          <small>{activity.statusLabel}</small>
          <span className="agent-activity-chevron" aria-hidden="true" />
        </button>
        <span className="agent-activity-actions">
          {issueNodes.length > 0 ? (
            <CopyButton label="Copy issues" value={activityIssueCopyText(issueNodes)} className="agent-activity-copy-action" />
          ) : null}
          <CopyMenu className="agent-activity-copy-menu" panelClassName="agent-activity-copy-menu-panel">
            <CopyButton label="Copy activity summary" value={activityCopyText(activity)} className="agent-activity-copy-action" />
            {activity.nodes.length > 0 ? (
              <CopyButton label="Copy activity details" value={activityDetailsCopyText(activity)} className="agent-activity-copy-action" />
            ) : null}
          </CopyMenu>
        </span>
      </div>
      <div
        className="agent-activity-digest"
        data-testid="agent-activity-digest"
        data-tone={activity.digest.tone}
        aria-label={digestLabel}
      >
        {showDigestLabel ? <span>{activity.digest.label}</span> : null}
        {showDigestLabel ? <span className="agent-activity-text-separator" aria-hidden="true"> · </span> : null}
        <strong>
          <HighlightText text={activity.digest.summary} query={searchQuery} />
        </strong>
        {activity.digest.meta.length > 0 ? (
          <span className="agent-activity-digest-meta">
            {activity.digest.meta.map((item) => (
              <span key={item} className="agent-activity-meta-wrap">
                <span className="agent-activity-text-separator" aria-hidden="true"> · </span>
                <small>{item}</small>
              </span>
            ))}
          </span>
        ) : null}
        {!open && activity.evidencePreview.length > 0 ? (
          <span className="agent-activity-digest-evidence" data-testid="agent-activity-digest-evidence">
            <span className="agent-activity-text-separator" aria-hidden="true"> · </span>
            <EvidenceChipList items={activity.evidencePreview} searchQuery={searchQuery} />
            {activity.evidenceAction && onUseAsDraft ? (
              <button
                type="button"
                className="agent-activity-digest-action"
                onClick={() => onUseAsDraft(activity.evidenceAction?.draft ?? "", activity.evidenceAction?.source)}
              >
                {activity.evidenceAction.label}
              </button>
            ) : null}
          </span>
        ) : null}
      </div>
      {open ? (
        <div className="agent-activity-body">
          {activity.brief.rows.length > 0 ? (
            <div className="agent-activity-brief" data-testid="agent-activity-brief">
              {activity.brief.rows.map((row) => (
                <ActivityBriefRow key={row.id} row={row} searchQuery={searchQuery} onUseAsDraft={onUseAsDraft} />
              ))}
            </div>
          ) : null}
          {activity.items.length > 0 ? (
            <div className="agent-activity-flow">
              {activity.items.map((item) => (
                <div key={item.id} className="agent-activity-item" data-kind={item.kind} data-tone={item.tone}>
                  <span className="agent-activity-dot" aria-hidden="true" />
                  <span className="agent-activity-copy">
                    <span className="agent-activity-label">{item.label}</span>
                    <strong>
                      <HighlightText text={item.title} query={searchQuery} />
                    </strong>
                    {item.detail ? (
                      <span className="agent-activity-detail">
                        <HighlightText text={item.detail} query={searchQuery} />
                      </span>
                    ) : null}
                  </span>
                  {item.meta ? <span className="agent-activity-meta">{item.meta}</span> : null}
                </div>
              ))}
            </div>
          ) : null}
          {activity.nodes.length > 0 ? (
            <div className="agent-activity-tree" data-testid="agent-activity-tree">
              {activity.nodes.map((node) => (
                <AgentActivityNode
                  key={node.id}
                  node={node}
                  openOverrides={openOverrides}
                  onOpenChange={setNodeOpen}
                  searchQuery={searchQuery}
                  onUseAsDraft={onUseAsDraft}
                />
              ))}
            </div>
          ) : null}
        </div>
      ) : null}
    </section>
  );
}

function activityCopyText(activity: TurnActivityView): string {
  const lines = [
    `${activity.title} (${activity.statusLabel})`,
    activity.digest.label && activity.digest.label !== activity.statusLabel
      ? `${activity.digest.label}: ${activity.digest.summary}`
      : activity.digest.summary,
    activity.digest.meta.length > 0 ? `Meta: ${activity.digest.meta.join(", ")}` : undefined,
    ...activity.brief.rows.map(activityBriefCopyLine),
    ...activity.nodes.flatMap(activityNodeCopyLines),
  ];
  return lines.filter((line): line is string => Boolean(line?.trim())).join("\n");
}

function activityBriefCopyLine(row: TurnActivityBriefRow): string {
  if ("evidence" in row) {
    return `${row.label}: ${row.evidence.map(evidenceCopyValue).join(", ")}`;
  }
  return `${row.label}: ${row.value}`;
}

function activityNodeCopyLines(node: TurnActivityNode): string[] {
  const indent = "  ".repeat(node.depth);
  const detail = node.detail ? ` - ${node.detail}` : "";
  const meta = node.meta ? ` (${node.meta})` : "";
  const evidence = node.evidence.length > 0 ? ` [${node.evidence.map(evidenceCopyValue).join(", ")}]` : "";
  return [
    `${indent}${node.label}: ${node.title}${detail}${meta}${evidence}`,
    ...node.children.flatMap(activityNodeCopyLines),
  ];
}

function activityDetailsCopyText(activity: TurnActivityView): string {
  const lines = [
    `${activity.title} (${activity.statusLabel})`,
    activity.digest.label && activity.digest.label !== activity.statusLabel
      ? `${activity.digest.label}: ${activity.digest.summary}`
      : activity.digest.summary,
    ...activity.nodes.flatMap((node, index) => activityNodeDetailCopyLines(node, String(index + 1))),
  ];
  return lines.filter((line): line is string => Boolean(line?.trim())).join("\n\n---\n\n");
}

function activityNodeBranchCopyText(node: TurnActivityNode): string {
  return activityNodeDetailCopyLines(node, "1").join("\n\n---\n\n");
}

function activityNodeDetailCopyLines(node: TurnActivityNode, path: string): string[] {
  return [
    [`# ${path} ${node.title}`, node.copyText].join("\n"),
    ...node.children.flatMap((child, index) => activityNodeDetailCopyLines(child, `${path}.${index + 1}`)),
  ];
}

function activityIssueNodes(nodes: readonly TurnActivityNode[]): TurnActivityNode[] {
  return nodes.flatMap((node) => [
    ...(node.status === "error" ? [node] : []),
    ...activityIssueNodes(node.children),
  ]);
}

function activityIssueCopyText(nodes: readonly TurnActivityNode[]): string {
  return nodes.map((node, index) => [`# issue ${index + 1}: ${node.title}`, node.copyText].join("\n")).join("\n\n---\n\n");
}

function evidenceCopyValue(item: TurnActivityEvidence): string {
  return `${item.label} ${item.displayValue || item.value}`;
}

function agentActivityDigestLabel(activity: TurnActivityView, showDigestLabel: boolean): string {
  const parts = [
    showDigestLabel ? activity.digest.label : undefined,
    activity.digest.summary,
    ...activity.digest.meta,
  ];
  return parts.filter(Boolean).join(" · ");
}

function ActivityBriefRow({
  row,
  searchQuery,
  onUseAsDraft,
}: {
  row: TurnActivityBriefRow;
  searchQuery?: string;
  onUseAsDraft?: UseAsDraft;
}) {
  return (
    <div className="agent-activity-brief-row" data-kind={row.id} data-tone={row.tone}>
      <span className="agent-activity-brief-label">{row.label}</span>
      {"evidence" in row ? (
        <>
          <span className="agent-activity-brief-evidence">
            <EvidenceChipList items={row.evidence} searchQuery={searchQuery} />
          </span>
          {row.action && onUseAsDraft ? (
            <button
              type="button"
              className="agent-activity-brief-action"
              onClick={() => onUseAsDraft(row.action?.draft ?? "", row.action?.source)}
            >
              {row.action.label}
            </button>
          ) : null}
        </>
      ) : (
        <>
          <strong>
            <HighlightText text={row.value} query={searchQuery} />
          </strong>
          {row.action && onUseAsDraft ? (
            <button
              type="button"
              className="agent-activity-brief-action"
              onClick={() => onUseAsDraft(row.action?.draft ?? "", row.action?.source)}
            >
              {row.action.label}
            </button>
          ) : null}
        </>
      )}
    </div>
  );
}

function EvidenceChipList({
  items,
  searchQuery,
  linkable = true,
}: {
  items: readonly TurnActivityEvidence[];
  searchQuery?: string;
  linkable?: boolean;
}) {
  return (
    <>
      {items.map((item, index) => (
        <span className="agent-activity-evidence-wrap" key={`${item.label}:${item.value}`}>
          {index > 0 ? <span className="agent-activity-evidence-text-separator"> </span> : null}
          <EvidenceChip item={item} searchQuery={searchQuery} linkable={linkable} />
        </span>
      ))}
    </>
  );
}

function EvidenceChip({
  item,
  searchQuery,
  linkable,
}: {
  item: TurnActivityEvidence;
  searchQuery?: string;
  linkable: boolean;
}) {
  const text = item.displayValue || item.value;
  const label = `${item.label}: ${item.value}`;
  const body = (
    <>
      <b>{item.label}</b>
      {" "}
      <HighlightText text={text} query={searchQuery} />
    </>
  );
  if (linkable && isHttpUrl(item.value)) {
    return (
      <a
        className="agent-activity-evidence-chip"
        href={item.value}
        title={item.value}
        aria-label={label}
        target="_blank"
        rel="noreferrer"
      >
        {body}
      </a>
    );
  }
  return (
    <span className="agent-activity-evidence-chip" title={item.value} aria-label={label}>
      {body}
    </span>
  );
}

function isHttpUrl(value: string): boolean {
  return /^https?:\/\//i.test(value);
}

function AgentActivityNode({
  node,
  openOverrides,
  onOpenChange,
  searchQuery,
  onUseAsDraft,
}: {
  node: TurnActivityNode;
  openOverrides: Record<string, boolean>;
  onOpenChange: (nodeId: string, open: boolean) => void;
  searchQuery?: string;
  onUseAsDraft?: UseAsDraft;
}) {
  const hasChildren = node.children.length > 0;
  const open = openOverrides[node.id] ?? node.autoOpen;
  const nextAction = nodeNextAction(node);

  function toggleOpen() {
    if (!hasChildren) return;
    onOpenChange(node.id, !open);
  }

  return (
    <div
      className="agent-activity-node"
      data-testid="agent-activity-node"
      data-tone={node.tone}
      data-status={node.status}
      data-depth={node.depth}
      data-open={open ? "true" : "false"}
      style={{ "--depth": node.depth } as CSSProperties}
    >
      <div className="agent-activity-node-row" data-testid="agent-activity-node-row" data-interactive={hasChildren ? "true" : "false"}>
        <span className="agent-activity-rail" aria-hidden="true" />
        {hasChildren ? (
          <button
            type="button"
            className="agent-activity-node-toggle"
            aria-expanded={open}
            aria-label={`${open ? "Collapse" : "Expand"} ${node.title}`}
            onClick={toggleOpen}
          >
            <span className="agent-activity-chevron" aria-hidden="true" />
          </button>
        ) : (
          <span className="agent-activity-node-toggle-spacer" aria-hidden="true" />
        )}
        <span className="agent-activity-dot" aria-hidden="true" />
        <span className="agent-activity-copy">
          <span className="agent-activity-label">{node.label}</span>
          <strong>
            <HighlightText text={node.title} query={searchQuery} />
          </strong>
          {node.detail ? (
            <span className="agent-activity-detail">
              <HighlightText text={node.detail} query={searchQuery} />
            </span>
          ) : null}
          {node.evidence.length > 0 ? (
            <span className="agent-activity-evidence" aria-label="Activity evidence">
              <EvidenceChipList items={node.evidence} searchQuery={searchQuery} />
            </span>
          ) : null}
        </span>
        <span className="agent-activity-node-actions">
          <CopyButton label="Copy step" value={node.copyText} className="agent-activity-node-action" />
          {hasChildren ? (
            <CopyButton label="Copy branch" value={activityNodeBranchCopyText(node)} className="agent-activity-node-action" />
          ) : null}
          {nextAction && onUseAsDraft ? (
            <button
              type="button"
              className="agent-activity-node-action"
              onClick={() => onUseAsDraft(nextAction.draft, "tool_guidance")}
            >
              Use this next step
            </button>
          ) : null}
        </span>
        {node.meta ? <span className="agent-activity-meta">{node.meta}</span> : null}
      </div>
      {open && hasChildren ? (
        <div className="agent-activity-children">
          {node.children.map((child) => (
            <AgentActivityNode
              key={child.id}
              node={child}
              openOverrides={openOverrides}
              onOpenChange={onOpenChange}
              searchQuery={searchQuery}
              onUseAsDraft={onUseAsDraft}
            />
          ))}
        </div>
      ) : null}
    </div>
  );
}

function nodeNextAction(node: TurnActivityNode): { draft: string } | undefined {
  const next = node.suggestedNext[0] ?? node.nextHint;
  if (!next?.trim()) return undefined;
  return { draft: `Continue: ${summarize(next, 160)}` };
}

interface FallbackAnswer {
  title: string;
  text: string;
}

function FallbackAnswerBubble({
  answer,
  searchQuery,
  onUseAsDraft,
}: {
  answer: FallbackAnswer;
  searchQuery?: string;
  onUseAsDraft?: UseAsDraft;
}) {
  const value = fallbackAnswerText(answer);
  return (
    <div className="flow-step flow-step-assistant fallback-answer" data-testid="fallback-answer">
      <div className="flow-text fallback-answer-copy">
        <strong>
          <HighlightText text={answer.title} query={searchQuery} />
        </strong>
        <span>
          <HighlightText text={answer.text} query={searchQuery} />
        </span>
      </div>
      <div className="message-actions">
        <CopyButton label="Copy output" value={value} className="message-action" />
        {onUseAsDraft ? (
          <button type="button" className="message-action" onClick={() => onUseAsDraft(fallbackDraft(answer), "result")}>
            Ask follow-up
          </button>
        ) : null}
      </div>
    </div>
  );
}

function fallbackAnswerText(answer: FallbackAnswer): string {
  return `${answer.title}\n${answer.text}`;
}

function fallbackDraft(answer: FallbackAnswer): string {
  return `Continue from this output: ${summarize(answer.text, 160)}`;
}

function buildFallbackAnswer(turn: TurnState, opts: { continuedAfterLimit?: boolean } = {}): FallbackAnswer | undefined {
  if (turn.assistantText || turn.status === "running" || turn.error) return undefined;
  if (opts.continuedAfterLimit) return undefined;
  if (turn.status === "max_turns") {
    return {
      title: "No final answer yet",
      text: "The runtime reached its action limit before synthesizing the final reply.",
    };
  }
  if (turn.status === "cancelled") {
    return {
      title: "Cancelled",
      text: "This request was stopped before a final answer.",
    };
  }
  const latestResult = latestToolResult(turn);
  if (!latestResult) return undefined;
  const resultText = latestResult.resultSummary ?? latestResult.result ?? "";
  const title = latestResult.status === "error"
    ? "Action failed"
    : latestResult.resultTruncated
      ? "Action output was truncated"
      : "Action output";
  const artifactHint = latestResult.resultTruncated && latestResult.resultArtifactPath
    ? "\nFull output is available below."
    : "";
  return {
    title,
    text: `${resultText || latestResult.resultArtifactPath}${artifactHint}`,
  };
}

function latestToolResult(turn: TurnState): ToolCallState | undefined {
  for (let index = turn.toolCalls.length - 1; index >= 0; index -= 1) {
    const call = turn.toolCalls[index];
    if (call.resultSummary || call.result || call.resultArtifactPath) return call;
  }
  return undefined;
}

function RunningAnswerBubble({
  turn,
  summary,
}: {
  turn: TurnState;
  summary: TurnWorkSummary;
}) {
  return (
    <div className="running-answer" data-testid="running-answer" role="status" aria-live="polite">
      <span className="pending-dots" aria-hidden="true">
        <i />
        <i />
        <i />
      </span>
      <span className="running-answer-copy">
        <strong>{runningAnswerTitle(turn)}</strong>
        <span>{runningAnswerDetail(turn)}</span>
      </span>
      {summary.items.length > 0 ? (
        <span className="running-answer-chips" aria-hidden="true">
          {summary.items.slice(0, 2).map((item) => (
            <span key={`${item.tone}-${item.label}`} data-tone={item.tone}>
              {item.label}
            </span>
          ))}
        </span>
      ) : null}
    </div>
  );
}

function runningAnswerTitle(turn: TurnState): string {
  if (turn.messageStreaming) return "Writing the answer";
  if (turn.thinkingStreaming || turn.thinkingText) return "Planning the next step";
  if (latestRunningTool(turn)) return "Working on this";
  return turn.userText ? "Reading your request" : "Starting";
}

function runningAnswerDetail(turn: TurnState): string {
  const tool = latestRunningTool(turn);
  if (tool) return summarize(currentToolFocus(tool), 96);
  if (turn.thinkingText) return summarize(turn.thinkingText, 96);
  if (turn.userText) return "Preparing the first step.";
  return "Preparing the first update.";
}

function latestRunningTool(turn: TurnState): ToolCallState | undefined {
  for (let index = turn.toolCalls.length - 1; index >= 0; index -= 1) {
    if (turn.toolCalls[index].status === "running") return turn.toolCalls[index];
  }
  return undefined;
}

function currentToolFocus(tool: ToolCallState): string {
  const task = stringArg(tool, "task") ?? stringArg(tool, "objective");
  if (task) return task;
  const command = stringArg(tool, "command");
  if (command) return command;
  const path = stringArg(tool, "path") ?? stringArg(tool, "file") ?? stringArg(tool, "filename");
  if (path) return path;
  return tool.tool;
}

function stringArg(tool: ToolCallState, key: string): string | undefined {
  const value = tool.args[key];
  return typeof value === "string" && value.trim() ? value.trim() : undefined;
}

function WorkDetails({
  turn,
  summary,
  events,
  searchQuery,
  searchMatch,
  sessionId,
  onOpenArtifact,
  onUseAsDraft,
  continuedAfterLimit,
  continuedIntoTurnNumber,
}: {
  turn: TurnState;
  summary: TurnWorkSummary;
  events: readonly NormalizedEvent[];
  searchQuery?: string;
  searchMatch?: boolean;
  sessionId?: string;
  onOpenArtifact?: (path: string) => void;
  onUseAsDraft?: UseAsDraft;
  continuedAfterLimit?: boolean;
  continuedIntoTurnNumber?: number;
}) {
  const autoOpen = shouldAutoOpenWorkDetails(Boolean(searchMatch));
  const heading = workThreadHeading(turn, { continuedAfterLimit, continuedIntoTurnNumber });
  const displaySummary = workSummaryDisplay(heading, summary);
  const label = workThreadLabel(heading, displaySummary);
  const threadStatus = workThreadStatus(turn);
  const userTouched = useRef(false);
  const [open, setOpen] = useState(autoOpen);

  useEffect(() => {
    if (!userTouched.current) setOpen(autoOpen);
  }, [autoOpen]);

  function toggle() {
    userTouched.current = true;
    setOpen((current) => !current);
  }

  return (
    <section className="work-thread" aria-label={label} data-testid="work-thread" data-open={open} data-status={threadStatus}>
      <button type="button" className="work-thread-head" aria-expanded={open} aria-label={label} onClick={toggle}>
        <span className="work-thread-copy">
          <span>{heading.title}</span>
          {heading.detail ? (
            <>
              {" "}
              <small>{heading.detail}</small>
            </>
          ) : null}
        </span>
        <WorkSummary summary={displaySummary} />
        <span className="work-chevron" aria-hidden="true" />
      </button>
      {open ? (
        <div className="work-thread-body">
          <ExecutionTree
            turn={turn}
            events={events}
            searchQuery={searchQuery}
            sessionId={sessionId}
            onOpenArtifact={onOpenArtifact}
            onUseAsDraft={onUseAsDraft}
          />
        </div>
      ) : null}
    </section>
  );
}

interface WorkSummaryDisplay {
  actionLabel?: string;
  items: WorkSummaryItem[];
}

function workSummaryDisplay(heading: { detail?: string }, summary: TurnWorkSummary): WorkSummaryDisplay {
  const detail = heading.detail ?? "";
  const actionLabel = isGenericActionCount(summary.actionLabel) && hasCallCount(detail)
    ? undefined
    : toolDetailActionLabel(summary.actionLabel);
  const items = summary.items.filter((item) => !detailAlreadyCoversItem(detail, item));
  return { actionLabel, items };
}

function isGenericActionCount(label: string): boolean {
  return /^\d+ actions?$/.test(label);
}

function toolDetailActionLabel(label: string): string {
  const match = label.match(/^(\d+) actions?$/);
  if (!match) return label;
  const count = Number(match[1]);
  return `${count} ${count === 1 ? "call" : "calls"}`;
}

function hasCallCount(detail: string): boolean {
  return /\b\d+\s+(?:completed\s+)?calls?\b/.test(detail);
}

function detailAlreadyCoversItem(detail: string, item: WorkSummaryItem): boolean {
  const escaped = item.label.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  return new RegExp(`(?:^|\\b)${escaped}(?:\\b|$)`).test(detail);
}

function workThreadLabel(heading: { title: string; detail?: string }, summary: WorkSummaryDisplay): string {
  const parts = [heading.title, heading.detail, summary.actionLabel, ...summary.items.map((item) => item.label)];
  return parts.filter(Boolean).join(" · ");
}

function workThreadStatus(turn: TurnState): TurnState["status"] {
  if (turn.status !== "completed" && latestFailedTool(turn)) return "error";
  return turn.status;
}

function workThreadHeading(turn: TurnState, opts: { continuedAfterLimit?: boolean; continuedIntoTurnNumber?: number } = {}): { title: string; detail?: string } {
  if (opts.continuedAfterLimit) {
    const detail = opts.continuedIntoTurnNumber ? `continued in message ${opts.continuedIntoTurnNumber}` : "continued later in this chat";
    return { title: "Action details", detail };
  }
  const failed = latestFailedTool(turn);
  if ((failed && turn.status !== "completed") || turn.status === "error" || turn.error) {
    const errorSummary = turn.error ? summarizeUserError(turn.error.code, turn.error.message) : undefined;
    return { title: "Action details", detail: failed ? `Issue: ${summarize(currentToolFocus(failed), 88)}` : errorSummary?.detail };
  }
  if (turn.status === "running") {
    return { title: "Action details", detail: runningAnswerDetail(turn) };
  }
  if (turn.status === "max_turns") {
    return { title: "Action details", detail: "Action limit reached" };
  }
  if (turn.status === "cancelled") {
    return { title: "Action details", detail: "Cancelled before a final answer" };
  }
  return { title: "Action details", detail: completedWorkDetail(turn) };
}

function completedWorkDetail(turn: TurnState): string | undefined {
  const count = turn.toolCalls.length;
  if (count === 0) return undefined;
  const failed = turn.toolCalls.filter((call) => call.status === "error").length;
  const calls = `${count} ${count === 1 ? "call" : "calls"}`;
  if (failed && turn.assistantText.trim()) return `${calls} · ${failed} tool issue${failed === 1 ? "" : "s"}`;
  if (failed) return `${failed} failed of ${calls}`;
  return `${count} completed ${count === 1 ? "call" : "calls"}`;
}

function latestFailedTool(turn: TurnState): ToolCallState | undefined {
  for (let index = turn.toolCalls.length - 1; index >= 0; index -= 1) {
    if (turn.toolCalls[index].status === "error") return turn.toolCalls[index];
  }
  return undefined;
}

function shouldAutoOpenWorkDetails(searchMatch: boolean): boolean {
  if (searchMatch) return true;
  return false;
}

function workSearchMatches(
  turn: TurnState,
  events: readonly NormalizedEvent[],
  searchQuery?: string,
): boolean {
  const query = normalizeSearch(searchQuery);
  if (!query) return false;
  const chunks = [
    ...buildExecutionTree(turn).flatMap(searchableExecutionNodeText),
    ...turn.toolCalls.flatMap((call) => [
      call.callId,
      call.tool,
      call.originalTool,
      call.originalArgsSummary,
      JSON.stringify(call.args),
      call.resultSummary,
      call.result,
      call.resultArtifactPath,
      ...(call.repairNotes ?? []),
    ]),
    ...events.filter(isToolEvent).map((event) => JSON.stringify(event.raw)),
  ];
  return normalizeSearch(chunks.filter(Boolean).join("\n")).includes(query);
}

function isToolEvent(event: NormalizedEvent): boolean {
  return event.type === EventType.ToolRequest || event.type === EventType.ToolResult;
}

function normalizeSearch(value?: string): string {
  return value?.trim().toLowerCase() ?? "";
}

function WorkSummary({ summary }: { summary: WorkSummaryDisplay }) {
  if (!summary.actionLabel && summary.items.length === 0) return null;
  return (
    <div className="work-summary" data-testid="work-summary" aria-label={[summary.actionLabel, ...summary.items.map((item) => item.label)].filter(Boolean).join(" · ")}>
      {summary.actionLabel ? (
        <span>{summary.actionLabel}</span>
      ) : null}
      {summary.items.map((item, index) => (
        <span className="work-summary-part" key={`${item.tone}-${item.label}`}>
          {summary.actionLabel || index > 0 ? <span className="work-summary-separator" aria-hidden="true"> · </span> : null}
          <WorkSummaryChip item={item} />
        </span>
      ))}
    </div>
  );
}

function WorkSummaryChip({ item }: { item: WorkSummaryItem }) {
  return (
    <span className="work-summary-chip" data-tone={item.tone}>
      {item.label}
    </span>
  );
}

function ArtifactStrip({
  artifacts,
  sessionId,
  onOpenArtifact,
  onUseAsDraft,
  searchQuery,
}: {
  artifacts: readonly TurnArtifact[];
  sessionId?: string;
  onOpenArtifact?: (path: string) => void;
  onUseAsDraft?: UseAsDraft;
  searchQuery?: string;
}) {
  return (
    <div className="artifact-strip" data-testid="turn-artifacts" aria-label="Files from this answer">
      {artifacts.map((artifact) => (
        <div key={artifact.path} className="artifact-pill">
          <div className="artifact-pill-copy">
            <span className="artifact-pill-label">{artifact.truncated ? "Full output" : "File"}</span>
            <strong title={artifact.path}>
              <HighlightText text={artifact.name} query={searchQuery} />
            </strong>
            <small title={artifact.source}>
              <HighlightText text={artifact.source} query={searchQuery} />
            </small>
          </div>
          <div className="artifact-pill-actions">
            {onUseAsDraft ? (
              <button type="button" className="artifact-pill-action" onClick={() => onUseAsDraft(artifactDraft(artifact.path), "artifact")}>
                Use in message
              </button>
            ) : null}
            {onOpenArtifact && sessionId ? (
              <button type="button" className="artifact-pill-action" onClick={() => onOpenArtifact(artifact.path)}>
                Open file
              </button>
            ) : null}
          </div>
        </div>
      ))}
    </div>
  );
}

function artifactDraft(path: string): string {
  return `Use this file in the next step: ${path}`;
}

function ContinuationPrompt({ turn, onUseAsDraft }: { turn: TurnState; onUseAsDraft?: UseAsDraft }) {
  const draft = continuationDraft(turn);
  const hasEvidence = turn.toolCalls.some((call) => call.status === "success" && (call.resultSummary || call.result || call.resultArtifactPath));
  return (
    <div className="continuation-card" data-testid="continuation-card">
      <div>
        <div className="continuation-title">Final answer not produced</div>
        <div className="continuation-copy">
          {hasEvidence
            ? "The runtime gathered evidence but stopped at its action limit before synthesizing a final reply."
            : "The runtime stopped at its action limit before it could produce a final reply."}
        </div>
      </div>
      {onUseAsDraft ? (
        <button type="button" className="node-action" onClick={() => onUseAsDraft(draft, "continuation")}>
          Ask for final answer
        </button>
      ) : null}
    </div>
  );
}

function continuationDraft(turn: TurnState): string {
  const task = turn.userText ? summarize(turn.userText, 120) : "";
  return task
    ? `Do not call more tools. Based only on the evidence already gathered in this chat, produce the final answer for: ${task}`
    : "Do not call more tools. Based only on the evidence already gathered in this chat, produce the final answer.";
}

function MessageStep({
  label,
  text,
  variant,
  streaming,
  searchQuery,
  onContinue,
  onReuse,
}: {
  label: string;
  text: string;
  variant: "user" | "assistant" | "thinking";
  streaming?: boolean;
  searchQuery?: string;
  onContinue?: UseAsDraft;
  onReuse?: UseAsDraft;
}) {
  return (
    <div
      className={`flow-step flow-step-${variant}`}
      data-streaming={streaming ? "true" : "false"}
      data-testid={`msg-${variant}`}
      role="group"
      aria-label={`${label} message`}
    >
      <div className={`flow-text${streaming ? " streaming-caret" : ""}`}>
        {variant === "assistant" ? (
          <MarkdownText text={text} query={searchQuery} />
        ) : (
          <HighlightText text={text} query={searchQuery} />
        )}
      </div>
      {variant === "assistant" && streaming ? (
        <div className="typing-tail" role="status" aria-live="polite">
          <span className="pending-dots" aria-hidden="true">
            <i />
            <i />
            <i />
          </span>
          <span>Writing</span>
        </div>
      ) : null}
      {variant === "assistant" ? (
        <div className="message-actions">
          <CopyMenu
            label="Copy answer"
            className="message-copy-menu"
            panelClassName="message-copy-menu-panel"
            triggerClassName="message-action"
          >
            <CopyButton label="Copy markdown" value={text} className="message-action" />
            <CopyButton label="Copy plain text" value={markdownToPlainText(text)} className="message-action" />
          </CopyMenu>
          {onContinue && !streaming ? (
            <button type="button" className="message-action" onClick={() => onContinue(answerDraft(text), "answer")}>
              Ask follow-up
            </button>
          ) : null}
        </div>
      ) : null}
      {variant === "user" && onReuse ? (
        <div className="message-actions">
          <CopyButton label="Copy message" value={text} className="message-action" />
          <button type="button" className="message-action" onClick={() => onReuse(text, "previous_message")}>
            Edit prompt
          </button>
        </div>
      ) : null}
    </div>
  );
}

function answerDraft(text: string): string {
  return `Continue from this answer: ${summarize(text, 160)}`;
}

function ErrorBlock({ error, onUseAsDraft }: { error: TurnError; onUseAsDraft?: UseAsDraft }) {
  const summary = summarizeUserError(error.code, error.message);
  const guidance = error.recoverable
    ? "You can continue from the message box below; the trace stays attached to this chat."
    : "Keep this chat for details, then start a new chat if the runtime cannot continue.";
  const diagnostic = errorDiagnosticText(error);

  return (
    <div className="error-card" role="alert" data-testid="error-card">
      <div className="error-main">
        <div>
          <div className="error-title">{summary.title}</div>
          <div className="error-message">
            <span className="error-code">{error.code}</span>
            <span>{summary.detail}</span>
          </div>
        </div>
        <span className="badge" data-kind={error.recoverable ? "repair" : "error"}>
          {error.recoverable ? "recoverable" : "stopped"}
        </span>
      </div>
      <div className="error-guidance">{guidance}</div>
      <div className="error-actions">
        {onUseAsDraft ? (
          <button type="button" className="node-action" onClick={() => onUseAsDraft(`Continue after ${error.code}: ${error.message}`, "error")}>
            Continue with this
          </button>
        ) : null}
        <CopyButton label="Copy diagnostic" value={diagnostic} />
      </div>
    </div>
  );
}

function errorDiagnosticText(error: TurnError): string {
  return [
    "Affent request error",
    `code: ${error.code}`,
    `recoverable: ${error.recoverable ? "yes" : "no"}`,
    `message: ${error.message}`,
  ].join("\n");
}

function humanTurnStatus(status: TurnState["status"], reason?: string, opts: { continuedAfterLimit?: boolean } = {}): string {
  if (status === "running") return "Working";
  if (status === "completed") return "Done";
  if (status === "max_turns" && opts.continuedAfterLimit) return "Continued";
  if (status === "max_turns") return "No final answer";
  if (status === "cancelled") return "Cancelled";
  if (status === "error") return "Blocked";
  return reason ?? status;
}

function turnTitle(turn: TurnState): string {
  if (turn.userText) return summarize(turn.userText, 72);
  if (turn.assistantText) return summarize(turn.assistantText, 72);
  return turn.id;
}

function summarize(text: string, limit: number): string {
  const singleLine = text.replace(/\s+/g, " ").trim();
  if (singleLine.length <= limit) return singleLine;
  return `${singleLine.slice(0, Math.max(0, limit - 1))}...`;
}

function eventBelongsToTurn(event: NormalizedEvent, turn: TurnState): boolean {
  if (event.turnId === turn.id) return true;
  return turn.toolCalls.some((call) => eventReferencesTool(event, call.callId));
}

function eventReferencesTool(event: NormalizedEvent, callId: ToolCallState["callId"]): boolean {
  return !!event.data && typeof event.data === "object" && (event.data as { call_id?: unknown }).call_id === callId;
}
