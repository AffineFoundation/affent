import { useState } from "react";

export function CopyButton({
  label,
  value,
  className = "node-action",
}: {
  label: string;
  value: string;
  className?: string;
}) {
  const [state, setState] = useState<"idle" | "copied" | "failed">("idle");

  async function copy() {
    try {
      await writeClipboard(value);
      setState("copied");
      window.setTimeout(() => setState("idle"), 1200);
    } catch {
      setState("failed");
      window.setTimeout(() => setState("idle"), 1600);
    }
  }

  return (
    <button type="button" className={className} onClick={() => void copy()}>
      {state === "copied" ? "Copied" : state === "failed" ? "Copy failed" : label}
    </button>
  );
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
  textarea.value = value;
  textarea.setAttribute("readonly", "");
  textarea.style.position = "fixed";
  textarea.style.left = "-9999px";
  textarea.style.top = "0";
  document.body.appendChild(textarea);
  textarea.focus();
  textarea.select();
  textarea.setSelectionRange(0, value.length);
  const copied = document.execCommand?.("copy") ?? false;
  textarea.remove();
  if (!copied) throw new Error("copy command failed");
}
