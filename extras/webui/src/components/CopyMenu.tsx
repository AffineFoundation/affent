import { useEffect, useRef, useState, type ReactNode } from "react";

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

  useEffect(() => {
    if (!open) return;
    const closeOnOutside = (event: PointerEvent) => {
      const target = event.target;
      if (target instanceof Node && rootRef.current?.contains(target)) return;
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

  return (
    <div ref={rootRef} className={className} data-open={open ? "true" : "false"}>
      <button
        type="button"
        className={`copy-menu-trigger${triggerClassName ? ` ${triggerClassName}` : ""}`}
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => setOpen((current) => !current)}
      >
        {label}
      </button>
      {open ? (
        <div className={panelClassName} role="menu">
          {children}
        </div>
      ) : null}
    </div>
  );
}
