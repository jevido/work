/**
 * Dragging a floating panel by its header.
 *
 * Every panel in the workbench keeps whatever CSS puts it on screen -- the
 * desk panel still opens bottom-left, the task panel still opens centred --
 * and this only adds an offset on top of that. Panels stop being nailed down
 * without any of them having to know where they started.
 */

/** How much of a panel has to stay inside its container, in px. */
const KEEP_VISIBLE = 56;

/**
 * The offset a panel has been dragged to.
 *
 * One of these per panel: hold it next to the state that opens the panel, put
 * `drag.transform` on the panel root and the attachment on its header.
 */
export class PanelDrag {
  x = $state(0);
  y = $state(0);
  /** True while a pointer is holding the header. */
  dragging = $state(false);

  /**
   * For the panel root: `style:transform={drag.transform}`.
   *
   * Empty until the panel is actually moved, so a panel that nobody drags is
   * laid out and painted exactly as it was before it became draggable.
   */
  transform = $derived(this.x === 0 && this.y === 0 ? "" : `translate(${this.x}px, ${this.y}px)`);

  /** Puts the panel back where it opened. */
  reset() {
    this.x = 0;
    this.y = 0;
  }
}

/**
 * Makes the element it is attached to the drag handle for `drag`'s panel.
 *
 * Used as `{@attach dragHandle(drag)}` on a panel header. The panel itself is
 * found by walking up from the handle, so a header does not have to be wired
 * to its own dialog by hand.
 */
export function dragHandle(drag: PanelDrag) {
  return (node: HTMLElement) => {
    let pointer: number | null = null;
    let originX = 0;
    let originY = 0;
    let fromX = 0;
    let fromY = 0;
    let minX = 0;
    let maxX = 0;
    let minY = 0;
    let maxY = 0;

    // A pointer that is dragging a window is not scrolling or selecting.
    node.style.touchAction = "none";
    node.style.cursor = "move";

    function onPointerDown(event: PointerEvent) {
      // Left button only, and never the controls sitting in the header: the
      // close button has to stay a button.
      if (event.button !== 0 || pointer !== null) return;
      if (event.target instanceof Element && event.target.closest("button, input, textarea, select, a")) {
        return;
      }

      const panel = node.closest<HTMLElement>("[role='dialog']") ?? node.parentElement;
      if (!panel) return;

      // Where the panel sits with no offset, measured now rather than
      // remembered: it may have been laid out against a window that has since
      // been resized.
      const rect = panel.getBoundingClientRect();
      const left = rect.left - drag.x;
      const top = rect.top - drag.y;

      // A fixed panel is bounded by the window; anything else by whatever it
      // is positioned inside, so the desk panel stays over the office.
      const parent = panel.offsetParent;
      const area =
        parent instanceof HTMLElement
          ? parent.getBoundingClientRect()
          : new DOMRect(0, 0, window.innerWidth, window.innerHeight);

      // Enough of the panel stays in view to grab it again, and the header
      // never goes above the top edge, where it could not be reached at all.
      minX = area.left + KEEP_VISIBLE - (left + rect.width);
      maxX = area.right - KEEP_VISIBLE - left;
      minY = area.top - top;
      maxY = area.bottom - KEEP_VISIBLE - top;

      pointer = event.pointerId;
      originX = event.clientX;
      originY = event.clientY;
      fromX = drag.x;
      fromY = drag.y;
      drag.dragging = true;
      // Keeps the pointer's events coming here even when it outruns the
      // header, and stops the drag from selecting the title under it.
      node.setPointerCapture(pointer);
      event.preventDefault();
    }

    function onPointerMove(event: PointerEvent) {
      if (pointer !== event.pointerId) return;
      drag.x = clamp(fromX + event.clientX - originX, minX, maxX);
      drag.y = clamp(fromY + event.clientY - originY, minY, maxY);
    }

    function onPointerUp(event: PointerEvent) {
      if (pointer !== event.pointerId) return;
      pointer = null;
      drag.dragging = false;
    }

    node.addEventListener("pointerdown", onPointerDown);
    node.addEventListener("pointermove", onPointerMove);
    node.addEventListener("pointerup", onPointerUp);
    node.addEventListener("pointercancel", onPointerUp);
    node.addEventListener("lostpointercapture", onPointerUp);

    return () => {
      node.removeEventListener("pointerdown", onPointerDown);
      node.removeEventListener("pointermove", onPointerMove);
      node.removeEventListener("pointerup", onPointerUp);
      node.removeEventListener("pointercancel", onPointerUp);
      node.removeEventListener("lostpointercapture", onPointerUp);
      drag.dragging = false;
    };
  };
}

function clamp(value: number, min: number, max: number): number {
  // A panel bigger than the space it is in has no range to keep it inside;
  // let it move freely rather than snapping it to an arbitrary edge.
  if (min > max) return value;
  return Math.min(Math.max(value, min), max);
}
