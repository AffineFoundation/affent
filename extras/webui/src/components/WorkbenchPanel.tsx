import type { ReactNode } from "react";

export type WorkbenchTab = "context" | "changes" | "run" | "files" | "workspace" | "automation" | "memory" | "skills" | "config" | "trace";

export interface WorkbenchNavItem {
  key: WorkbenchTab;
  label: string;
  detail: string;
  badge?: string;
  tone?: "error" | "warning" | "attention";
}

export function WorkbenchPanel({
  title,
  subtitle,
  navItems,
  activeTab,
  onSelectTab,
  onClose,
  children,
}: {
  title: string;
  subtitle: string;
  navItems: readonly WorkbenchNavItem[];
  activeTab: WorkbenchTab;
  onSelectTab: (tab: WorkbenchTab) => void;
  onClose: () => void;
  children: ReactNode;
}) {
  return (
    <aside className="workbench-panel" data-testid="workbench-panel" aria-label={title}>
      <div className="workbench-panel-head">
        <div>
          <strong>{title}</strong>
          <span>{subtitle}</span>
        </div>
        <button type="button" className="workbench-close" aria-label={`Close ${title}`} onClick={onClose}>
          Close
        </button>
      </div>
      <nav className="workbench-nav" aria-label={`${title} sections`}>
        {navItems.map((item) => (
          <button
            key={item.key}
            type="button"
            className="workbench-nav-item"
            data-active={activeTab === item.key ? "true" : "false"}
            data-tone={item.tone}
            onClick={() => onSelectTab(item.key)}
          >
            <span className="workbench-nav-main">
              <strong>{item.label}</strong>
              <small>{item.detail}</small>
            </span>
            {item.badge ? <span className="workbench-nav-badge">{item.badge}</span> : null}
          </button>
        ))}
      </nav>
      <div className="workbench-tab-surface" data-testid="workbench-tab-surface">
        {children}
      </div>
    </aside>
  );
}

export function WorkbenchEmpty({ title, detail }: { title: string; detail: string }) {
  return (
    <section className="workbench-empty" data-testid="workbench-empty">
      <strong>{title}</strong>
      <span>{detail}</span>
    </section>
  );
}
