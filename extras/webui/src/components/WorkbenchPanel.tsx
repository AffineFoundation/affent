import type { ReactNode } from "react";
import type { WorkbenchNavItem, WorkbenchNavScope, WorkbenchTab } from "../view/workbenchNav";

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
  const groups = groupNavItems(navItems);

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
        {groups.map((group) => (
          <div key={group.scope} className="workbench-nav-group" data-scope={group.scope}>
            <span className="workbench-nav-group-label">{group.label}</span>
            <div className="workbench-nav-group-items">
              {group.items.map((item) => (
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
            </div>
          </div>
        ))}
      </nav>
      <div className="workbench-tab-surface" data-testid="workbench-tab-surface">
        {children}
      </div>
    </aside>
  );
}

function groupNavItems(items: readonly WorkbenchNavItem[]): { scope: WorkbenchNavScope; label: string; items: WorkbenchNavItem[] }[] {
  const current = items.filter((item) => item.scope === "current");
  const platform = items.filter((item) => item.scope === "platform");
  const groups: { scope: WorkbenchNavScope; label: string; items: WorkbenchNavItem[] }[] = [
    { scope: "current", label: "Current work", items: current },
    { scope: "platform", label: "Platform", items: platform },
  ];
  return groups.filter((group) => group.items.length > 0);
}

export function WorkbenchEmpty({ title, detail }: { title: string; detail: string }) {
  return (
    <section className="workbench-empty" data-testid="workbench-empty">
      <strong>{title}</strong>
      <span>{detail}</span>
    </section>
  );
}
