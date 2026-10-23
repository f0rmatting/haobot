import { useEffect, useLayoutEffect, useRef, useState, type ReactNode, type RefObject } from "react";
import { createPortal } from "react-dom";

const margin = 8;

// Popover floats a panel next to an anchor, outside any scrolling or
// clipping container: it opens below the anchor when there is room, above
// otherwise, and is kept inside the viewport.
export function Popover({ anchor, open, onClose, children, width = 256, align = "start" }: { anchor: RefObject<HTMLElement | null>; open: boolean; onClose: () => void; children: ReactNode; width?: number; align?: "start" | "end" }) {
  const panel = useRef<HTMLDivElement>(null);
  const [pos, setPos] = useState<{ top: number; left: number; maxHeight: number } | null>(null);

  useLayoutEffect(() => {
    if (!open) {
      setPos(null);
      return;
    }
    const place = () => {
      const a = anchor.current?.getBoundingClientRect();
      const p = panel.current;
      if (!a || !p) return;
      const vw = window.innerWidth;
      const vh = window.innerHeight;
      const w = Math.min(width, vw - 2 * margin);
      const h = p.scrollHeight;
      const below = vh - a.bottom - margin;
      const above = a.top - margin;
      const openUp = h > below && above > below;
      const room = openUp ? above : below;
      const maxHeight = Math.max(120, room - 4);
      const top = openUp ? Math.max(margin, a.top - Math.min(h, maxHeight) - 4) : a.bottom + 4;
      let left = align === "end" ? a.right - w : a.left;
      left = Math.min(Math.max(margin, left), vw - w - margin);
      setPos({ top, left, maxHeight });
    };
    place();
    window.addEventListener("resize", place);
    return () => window.removeEventListener("resize", place);
  }, [open, anchor, width, align, children]);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    const onDown = (e: MouseEvent) => {
      const t = e.target as Node;
      if (!panel.current?.contains(t) && !anchor.current?.contains(t)) onClose();
    };
    // Scrolling the page would detach the panel from its anchor; close it.
    const onScroll = (e: Event) => {
      if (!panel.current?.contains(e.target as Node)) onClose();
    };
    window.addEventListener("keydown", onKey);
    window.addEventListener("mousedown", onDown);
    window.addEventListener("scroll", onScroll, true);
    return () => {
      window.removeEventListener("keydown", onKey);
      window.removeEventListener("mousedown", onDown);
      window.removeEventListener("scroll", onScroll, true);
    };
  }, [open, onClose, anchor]);

  if (!open) return null;
  return createPortal(
    <div
      ref={panel}
      role="menu"
      className="animate-in scroll-thin fixed z-[70] overflow-y-auto rounded-2xl bg-panel-solid p-2 shadow-2xl ring-1 ring-line"
      style={{ top: pos?.top ?? -9999, left: pos?.left ?? -9999, width: Math.min(width, window.innerWidth - 2 * margin), maxHeight: pos?.maxHeight, visibility: pos ? "visible" : "hidden" }}
    >
      {children}
    </div>,
    document.body,
  );
}
