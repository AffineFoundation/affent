import { useEffect, useRef, useState } from "react";

export function CopyButton({
  label,
  value,
  className = "node-action",
  displayLabel,
  title,
  icon,
}: {
  label: string;
  value: string;
  className?: string;
  displayLabel?: string;
  title?: string;
  icon?: string;
}) {
  const [state, setState] = useState<"idle" | "copied" | "failed">("idle");
  const resetTimer = useRef<number | undefined>(undefined);

  useEffect(() => () => {
    if (resetTimer.current != null) window.clearTimeout(resetTimer.current);
  }, []);

  async function copy() {
    if (resetTimer.current != null) window.clearTimeout(resetTimer.current);
    try {
      await writeClipboard(value);
      setState("copied");
      resetTimer.current = window.setTimeout(() => setState("idle"), 1200);
    } catch {
      setState("failed");
      resetTimer.current = window.setTimeout(() => setState("idle"), 1600);
    }
  }

  return (
    <button
      type="button"
      className={className}
      aria-label={displayLabel || icon ? label : undefined}
      data-copy-state={state}
      data-icon={icon}
      disabled={value.length === 0}
      title={title ?? label}
      onClick={() => void copy()}
    >
      {icon ? <span className="visually-hidden">{copyButtonText(state, label, displayLabel)}</span> : copyButtonText(state, label, displayLabel)}
    </button>
  );
}

function copyButtonText(state: "idle" | "copied" | "failed", label: string, displayLabel: string | undefined): string {
  if (state === "copied") return displayLabel ? "OK" : "Copied";
  if (state === "failed") return displayLabel ? "!" : "Copy failed";
  return displayLabel ?? label;
}

async function writeClipboard(value: string): Promise<void> {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(value);
      return;
    }
  } catch {
    // Fall back for browsers where the Clipboard API is blocked by context
    // or permissions. The caller will surface failure if this path also fails.
  }
  fallbackCopy(value);
}

function fallbackCopy(value: string): void {
  const textarea = document.createElement("textarea");
  const activeElement = document.activeElement instanceof HTMLElement ? document.activeElement : undefined;
  const scrollX = window.scrollX;
  const scrollY = window.scrollY;
  textarea.value = value;
  textarea.setAttribute("aria-hidden", "true");
  textarea.setAttribute("readonly", "");
  textarea.style.position = "fixed";
  textarea.style.left = "0";
  textarea.style.top = "0";
  textarea.style.width = "1px";
  textarea.style.height = "1px";
  textarea.style.opacity = "0";
  textarea.style.pointerEvents = "none";
  document.body.appendChild(textarea);
  try {
    textarea.focus({ preventScroll: true });
    textarea.select();
    textarea.setSelectionRange(0, value.length);
    const copied = document.execCommand?.("copy") ?? false;
    if (!copied) throw new Error("copy command failed");
  } finally {
    textarea.remove();
    try {
      activeElement?.focus({ preventScroll: true });
    } catch {
      activeElement?.focus();
    }
    if (window.scrollX !== scrollX || window.scrollY !== scrollY) {
      window.scrollTo(scrollX, scrollY);
    }
  }
}
