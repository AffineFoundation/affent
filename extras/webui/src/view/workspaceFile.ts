import type { SessionFileEntry, SessionFileResponse } from "../api/sessions";
import { formatByteCount } from "./byteFormat";

export interface WorkspaceFileEntryView {
  name: string;
  path: string;
  kind: "file" | "directory";
  size?: string;
  modTime?: string;
}

export interface WorkspaceFileView {
  path: string;
  kind: "file" | "directory";
  title: string;
  detail: string;
  entries: WorkspaceFileEntryView[];
  text?: string;
  lines: string[];
  hasMore: boolean;
  size?: string;
}

export type WorkspaceFileScope = "global" | "session";

export type WorkspaceFileBrowserState =
  | { state: "idle"; workspacePath?: string; scope?: WorkspaceFileScope }
  | { state: "loading"; path: string; workspacePath?: string; scope?: WorkspaceFileScope }
  | { state: "ready"; file: WorkspaceFileView; revealDirectories?: WorkspaceFileView[]; workspacePath?: string; scope?: WorkspaceFileScope }
  | { state: "error"; path?: string; error: string; workspacePath?: string; scope?: WorkspaceFileScope };

export function buildWorkspaceFileView(resp: SessionFileResponse): WorkspaceFileView {
  const kind = resp.kind === "directory" ? "directory" : "file";
  const path = cleanPath(resp.path);
  const entries = (resp.entries ?? []).map(workspaceFileEntryView);
  const text = kind === "file" ? resp.text ?? "" : undefined;
  return {
    path,
    kind,
    title: kind === "directory" ? directoryTitle(path) : fileTitle(path),
    detail: kind === "directory" ? directoryDetail(entries, Boolean(resp.has_more)) : fileDetail(resp),
    entries,
    text,
    lines: text == null ? [] : text.replace(/\r\n/g, "\n").split("\n"),
    hasMore: Boolean(resp.has_more),
    size: resp.bytes != null ? formatByteCount(resp.bytes) : undefined,
  };
}

export function workspaceFileDraft(file: WorkspaceFileView): string {
  if (file.kind === "directory") {
    return [
      "Use this workspace directory listing in the next step:",
      `Path: ${file.path}`,
      file.entries.slice(0, 40).map((entry) => `- ${entry.kind === "directory" ? "dir" : "file"} ${entry.path}${entry.size ? ` (${entry.size})` : ""}`).join("\n"),
      file.hasMore ? "- ... additional entries omitted from the browser preview" : undefined,
    ].filter((line): line is string => Boolean(line)).join("\n");
  }
  return [
    "Use this workspace file snapshot in the next step:",
    `Path: ${file.path}`,
    file.size ? `Size: ${file.size}` : undefined,
    file.hasMore ? "Snapshot is truncated; read more before making broad edits." : undefined,
    "",
    file.text ?? "",
  ].filter((line): line is string => Boolean(line)).join("\n");
}

export function workspaceFileRangeText(file: WorkspaceFileView, startLine: number, endLine: number): string {
  const start = Math.max(1, Math.min(startLine, endLine));
  const end = Math.min(file.lines.length, Math.max(startLine, endLine));
  return [
    `Workspace file range for ${file.path}`,
    `Lines: ${start}-${end}`,
    "",
    file.lines.slice(start - 1, end).join("\n"),
  ].join("\n");
}

export function workspaceFileRangeDraft(
  file: WorkspaceFileView,
  startLine: number,
  endLine: number,
  intent: "ask" | "edit",
): string {
  const start = Math.max(1, Math.min(startLine, endLine));
  const end = Math.min(file.lines.length, Math.max(startLine, endLine));
  const lead = intent === "edit"
    ? "Edit this selected workspace file range in the next step:"
    : "Review this selected workspace file range in the next step:";
  return [
    lead,
    `Path: ${file.path}`,
    `Lines: ${start}-${end}`,
    file.hasMore ? "Snapshot is truncated; read more before making broad edits." : undefined,
    "",
    file.lines.slice(start - 1, end).join("\n"),
  ].filter((line): line is string => Boolean(line)).join("\n");
}

export function parentWorkspacePath(path: string): string | undefined {
  const clean = cleanPath(path);
  if (clean === ".") return undefined;
  const parts = clean.split("/").filter(Boolean);
  parts.pop();
  return parts.length ? parts.join("/") : ".";
}

function workspaceFileEntryView(entry: SessionFileEntry): WorkspaceFileEntryView {
  const kind = entry.kind === "directory" ? "directory" : "file";
  return {
    name: entry.name,
    path: cleanPath(entry.path),
    kind,
    size: kind === "file" && entry.bytes != null ? formatByteCount(entry.bytes) : undefined,
    modTime: entry.mod_time,
  };
}

function cleanPath(value: string | undefined): string {
  const clean = value?.trim().replace(/\\/g, "/").replace(/^\/+/, "") || ".";
  return clean === "" ? "." : clean;
}

function directoryTitle(path: string): string {
  return path === "." ? "Workspace root" : path;
}

function fileTitle(path: string): string {
  return path.split("/").filter(Boolean).at(-1) ?? path;
}

function directoryDetail(entries: readonly WorkspaceFileEntryView[], hasMore: boolean): string {
  const dirs = entries.filter((entry) => entry.kind === "directory").length;
  const files = entries.length - dirs;
  const parts = [
    dirs > 0 ? `${dirs} ${dirs === 1 ? "directory" : "directories"}` : undefined,
    files > 0 ? `${files} ${files === 1 ? "file" : "files"}` : undefined,
  ].filter(Boolean);
  const base = parts.length ? parts.join(" · ") : "Empty directory";
  return hasMore ? `${base} · more available` : base;
}

function fileDetail(resp: SessionFileResponse): string {
  const parts = [
    resp.bytes != null ? formatByteCount(resp.bytes) : undefined,
    resp.has_more ? "preview truncated" : "loaded",
  ].filter(Boolean);
  return parts.join(" · ");
}
