import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { Children, isValidElement, type ReactNode } from "react";
import { CopyButton } from "./CopyButton";
import { HighlightText } from "./HighlightText";

export function MarkdownText({ text, query }: { text: string; query?: string }) {
  if (query?.trim()) {
    return <HighlightText text={text} query={query} />;
  }
  return (
    <div className="markdown-text">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          table({ children, ...props }) {
            const tableText = tableCopyText(children);
            return (
              <div className="markdown-table-scroll">
                <div className="markdown-table-head">
                  <span>Table</span>
                  <CopyButton label="Copy table" value={tableText} className="markdown-table-copy" />
                </div>
                <table {...props}>{children}</table>
              </div>
            );
          },
          pre({ children, ...props }) {
            const code = nodeText(children).replace(/\n$/, "");
            const label = codeBlockLabel(children);
            const copyLabel = label === "Code" ? "Copy code" : `Copy ${label.toLowerCase()} code`;
            return (
              <div className="markdown-code-block">
                <div className="markdown-code-head">
                  <span>{label}</span>
                  <CopyButton label={copyLabel} value={code} className="markdown-code-copy" />
                </div>
                <pre {...props}>{children}</pre>
              </div>
            );
          },
          a({ children, href, ...props }) {
            const external = isExternalHref(href);
            return (
              <a
                {...props}
                href={href}
                title={external ? href : props.title}
                target={external ? "_blank" : props.target}
                rel={external ? "noreferrer" : props.rel}
              >
                {external && isBareUrlLabel(href, children) ? readableUrl(href) : children}
              </a>
            );
          },
        }}
      >
        {text}
      </ReactMarkdown>
    </div>
  );
}

function isExternalHref(href?: string): href is string {
  return /^https?:\/\//i.test(href ?? "");
}

function isBareUrlLabel(href: string, children: ReactNode): boolean {
  const label = nodeText(children).trim();
  if (!label) return false;
  return normalizeUrlLabel(label) === normalizeUrlLabel(href);
}

function nodeText(node: ReactNode): string {
  if (typeof node === "string" || typeof node === "number") return String(node);
  if (Array.isArray(node)) return node.map(nodeText).join("");
  if (isValidElement<{ children?: ReactNode }>(node)) return nodeText(node.props.children);
  return Children.toArray(node).map(nodeText).join("");
}

function codeBlockLabel(children: ReactNode): string {
  const language = findCodeLanguage(children);
  if (!language) return "Code";
  return readableCodeLanguage(language);
}

function findCodeLanguage(node: ReactNode): string | undefined {
  if (Array.isArray(node)) {
    for (const child of node) {
      const language = findCodeLanguage(child);
      if (language) return language;
    }
    return undefined;
  }
  if (!isValidElement<{ className?: string; children?: ReactNode }>(node)) return undefined;
  const className = node.props.className;
  const match = typeof className === "string" ? /\blanguage-([A-Za-z0-9_-]+)/.exec(className) : undefined;
  if (match?.[1]) return match[1];
  return findCodeLanguage(node.props.children);
}

function readableCodeLanguage(language: string): string {
  const normalized = language.toLowerCase();
  const known: Record<string, string> = {
    bash: "Shell",
    sh: "Shell",
    shell: "Shell",
    zsh: "Shell",
    json: "JSON",
    js: "JavaScript",
    jsx: "JSX",
    ts: "TypeScript",
    tsx: "TSX",
    py: "Python",
    python: "Python",
    yaml: "YAML",
    yml: "YAML",
    html: "HTML",
    css: "CSS",
    sql: "SQL",
  };
  return known[normalized] ?? normalized.replace(/(^|[-_])([a-z0-9])/g, (_match, prefix: string, char: string) =>
    `${prefix ? " " : ""}${char.toUpperCase()}`,
  );
}

function tableCopyText(children: ReactNode): string {
  return collectTableRows(children)
    .map((row) => row.map(cleanCellText).join("\t"))
    .filter((row) => row.trim())
    .join("\n");
}

function collectTableRows(node: ReactNode): string[][] {
  if (Array.isArray(node)) return node.flatMap(collectTableRows);
  if (!isValidElement<{ children?: ReactNode }>(node)) return [];
  if (typeof node.type === "string" && node.type.toLowerCase() === "tr") {
    const cells = Children.toArray(node.props.children).flatMap((child) =>
      isTableCell(child) ? [nodeText(child).trim()] : [],
    );
    return cells.length > 0 ? [cells] : [];
  }
  return collectTableRows(node.props.children);
}

function isTableCell(node: ReactNode): boolean {
  if (!isValidElement(node) || typeof node.type !== "string") return false;
  const type = node.type.toLowerCase();
  return type === "td" || type === "th";
}

function cleanCellText(text: string): string {
  return text.replace(/\s+/g, " ").trim();
}

function normalizeUrlLabel(value: string): string {
  return value.replace(/\/+$/, "").trim();
}

function readableUrl(value: string): string {
  try {
    const url = new URL(value);
    const host = url.hostname.replace(/^www\./, "");
    const path = url.pathname.replace(/\/+$/, "");
    if (!path || path === "/") return host;
    const segments = path.split("/").filter(Boolean).slice(0, 2);
    return `${host}/${segments.join("/")}`;
  } catch {
    return value;
  }
}
