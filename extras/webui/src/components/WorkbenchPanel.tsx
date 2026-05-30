import type { PointerEventHandler, ReactNode } from "react";
import type { WorkbenchAttachment } from "../view/workbenchContext";
import type { WorkbenchNavItem, WorkbenchNavScope, WorkbenchTab } from "../view/workbenchNav";

export function WorkbenchPanel({
  title,
  subtitle,
  attachment,
  navItems,
  activeTab,
  onSelectTab,
  onResizeStart,
  onClose,
  children,
}: {
  title: string;
  subtitle: string;
  attachment?: WorkbenchAttachment;
  navItems: readonly WorkbenchNavItem[];
  activeTab: WorkbenchTab;
  onSelectTab: (tab: WorkbenchTab) => void;
  onResizeStart?: PointerEventHandler<HTMLSpanElement>;
  onClose: () => void;
  children?: ReactNode;
}) {
  const groups = groupNavItems(navItems);

  return (
    <aside className="workbench-panel" data-active-tab={activeTab} data-testid="workbench-panel" aria-label={title}>
      {onResizeStart ? (
        <span
          className="workbench-resize-handle"
          role="separator"
          aria-label="Resize Workbench"
          aria-orientation="vertical"
          onPointerDown={onResizeStart}
        />
      ) : null}
      <div className="workbench-panel-head">
        <div>
          <strong>{title}</strong>
          <span>{subtitle}</span>
        </div>
        <button type="button" className="workbench-close" aria-label={`Close ${title}`} onClick={onClose}>
          Close
        </button>
      </div>
      {attachment ? (
        <section className="workbench-attachment" data-tone={attachment.tone ?? "none"} data-testid="workbench-attachment" aria-label={attachment.label}>
          <div className="workbench-attachment-main">
            <span>{attachment.label}</span>
            <strong title={attachment.title}>{attachment.title}</strong>
          </div>
          {attachment.metrics?.length ? (
            <div className="workbench-attachment-metrics" aria-label={`${attachment.label} status`}>
              {attachment.metrics.map((metric) => (
                <span key={metric}>{metric}</span>
              ))}
            </div>
          ) : null}
        </section>
      ) : null}
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
                  data-tone={item.tone === "error" ? "error" : undefined}
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
      {children ? (
        <div className="workbench-tab-surface" data-tab={activeTab} data-testid="workbench-tab-surface">
          {children}
        </div>
      ) : null}
    </aside>
  );
}

function groupNavItems(items: readonly WorkbenchNavItem[]): { scope: WorkbenchNavScope; label: string; items: WorkbenchNavItem[] }[] {
  const current = items.filter((item) => item.scope === "current");
  const platform = items.filter((item) => item.scope === "platform");
  const groups: { scope: WorkbenchNavScope; label: string; items: WorkbenchNavItem[] }[] = [
    { scope: "current", label: "Session", items: current },
    { scope: "platform", label: "Global", items: platform },
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
