import { createPortal } from "react-dom";
import { useEffect, useLayoutEffect, useRef, useState, type CSSProperties, type ReactNode } from "react";

export function CopyMenu({
  label = "Copy",
  className,
  panelClassName,
  triggerClassName,
  children,
}: {
  label?: string;
  className: string;
  panelClassName: string;
  triggerClassName?: string;
  children: ReactNode;
}) {
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement | null>(null);
  const triggerRef = useRef<HTMLButtonElement | null>(null);
  const panelRef = useRef<HTMLDivElement | null>(null);
  const [panelStyle, setPanelStyle] = useState<CSSProperties | undefined>();

  useEffect(() => {
    if (!open) return;
    const closeOnOutside = (event: PointerEvent) => {
      const target = event.target;
      if (target instanceof Node && (rootRef.current?.contains(target) || panelRef.current?.contains(target))) return;
      setOpen(false);
    };
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    document.addEventListener("pointerdown", closeOnOutside);
    document.addEventListener("keydown", closeOnEscape);
    return () => {
      document.removeEventListener("pointerdown", closeOnOutside);
      document.removeEventListener("keydown", closeOnEscape);
    };
  }, [open]);

  useLayoutEffect(() => {
    if (!open) {
      setPanelStyle(undefined);
      return;
    }
    const updatePosition = () => {
      const trigger = triggerRef.current;
      if (!trigger) return;
      const rect = trigger.getBoundingClientRect();
      const padding = 8;
      const gap = 6;
      const maxWidth = 280;
      const estimatedHeight = panelRef.current?.getBoundingClientRect().height ?? 120;
      const roomBelow = window.innerHeight - rect.bottom - gap - padding;
      const openAbove = roomBelow < estimatedHeight && rect.top > estimatedHeight + gap + padding;
      const top = openAbove ? Math.max(padding, rect.top - gap - estimatedHeight) : rect.bottom + gap;
      const left = Math.max(padding, Math.min(rect.left, window.innerWidth - maxWidth - padding));
      setPanelStyle({
        position: "fixed",
        top,
        left,
        right: "auto",
        zIndex: 10000,
        marginTop: 0,
        maxWidth: `min(${maxWidth}px, calc(100vw - ${padding * 2}px))`,
      });
    };

    updatePosition();
    const frame = window.requestAnimationFrame(updatePosition);
    window.addEventListener("resize", updatePosition);
    window.addEventListener("scroll", updatePosition, true);
    return () => {
      window.cancelAnimationFrame(frame);
      window.removeEventListener("resize", updatePosition);
      window.removeEventListener("scroll", updatePosition, true);
    };
  }, [open]);

  return (
    <div ref={rootRef} className={className} data-open={open ? "true" : "false"}>
      <button
        ref={triggerRef}
        type="button"
        className={`copy-menu-trigger${triggerClassName ? ` ${triggerClassName}` : ""}`}
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => setOpen((current) => !current)}
      >
        {label}
      </button>
      {open ? (
        createPortal(
          <div
            ref={panelRef}
            className={panelClassName}
            role="menu"
            style={panelStyle}
            onPointerDown={(event) => event.stopPropagation()}
            onClick={() => setOpen(false)}
          >
            {children}
          </div>,
          document.body,
        )
      ) : null}
    </div>
  );
}
