import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { Children, isValidElement, type ReactNode } from "react";
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
            return (
              <div className="markdown-table-scroll">
                <table {...props}>{children}</table>
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
