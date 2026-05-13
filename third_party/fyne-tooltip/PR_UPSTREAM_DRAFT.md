# Draft: Upstream PR for fyne-tooltip (benign nil / teardown handling)

**Suggested title**

`fix: avoid LogError on nil canvas and missing tooltip layers (teardown / overlay races)`

**Target repository:** https://github.com/dweymouth/fyne-tooltip  

---

## Summary

Tooltip display uses a **delayed** path (`time.After` + `fyne.Do` → `showToolTip`). By the time the delay elapses, the hovered widget may no longer be on a canvas (`CanvasForObject` returns `nil`), or the active overlay may not have a dedicated tooltip sub-layer. Today the library reports these cases with **`fyne.LogError`**, which is noisy in production and misclassifies **expected** lifecycle behavior as errors.

This draft proposes treating those paths as **no-op** (skip showing the tooltip) without logging at error level, and falling back to the **root window** tooltip layer when an overlay has no mapped sub-layer so tooltips still work for common Fyne dialog stacks.

---

## Problem we hit in real apps

1. User hovers a tooltip-enabled control.
2. Before the tooltip delay fires, the user switches tabs, closes a dialog, replaces `Window` content, or the window begins teardown.
3. `showToolTip` runs with a widget that no longer has a canvas → upstream passes `nil` into `ShowToolTipAtMousePosition` → **log:** `no canvas associated with tool tip widget`.

Similarly:

- `NewPopUpToolTipLayer` when the parent canvas never had `AddWindowToolTipLayer` (or canvas was destroyed) → **log:** `no tool tip layer created for parent canvas`.
- `findToolTipLayer` when the current overlay is not registered → **log:** `no tool tip layer created for current overlay`, even though falling back to the root layer would often work.

**Application code cannot fully prevent (1–3):** the race is inherent to async tooltip display.

---

## Proposed behavior changes

### 1. `internal.ShowToolTipAtMousePosition`

- If `canvas == nil`, **return `nil` without** `fyne.LogError` (optionally `fyne.LogDebug` if the project adopts a debug channel later).
- Rationale: caller already handles `nil` handle; this is a normal “widget left the tree” outcome.

### 2. `widget.toolTipContext.showToolTip` (or equivalent)

- If `wid == nil`, return early (do not call `ShowToolTipAtMousePosition`).
- After `canvas := CanvasForObject(wid)`, if `canvas == nil`, return early (same rationale as (1)).

### 3. `internal.NewPopUpToolTipLayer`

- If there is no parent tooltip layer for `popUp.Canvas`, **return `nil` without** `fyne.LogError`.
- `AddPopUpToolTipLayer` already can no-op when `l == nil`; callers that forget the main window layer get silent failure instead of log spam.

### 4. `internal.findToolTipLayer`

- When `handle.overlay != nil` but `tl.overlays[handle.overlay]` is missing, **fall back** to the root window’s `ToolTipLayer` (try showing on the main layer) instead of logging and returning `nil`.
- If you prefer strict mode for some consumers, this could be behind a package-level option; default “quiet + fallback” matches typical GUI expectations.

### 5. `ToolTipWidgetExtend.MouseIn`

- If `ExtendToolTipWidget` was not called and `Obj` is nil, **do not** schedule a tooltip (avoids a nil `CanvasForObject` path). Optional: keep existing `LogError` for `ToolTipWidget` when `wid` is nil if you want to catch programmer error there.

---

## Non-goals

- Changing default tooltip delay or positioning.
- Removing `LogError` for true programmer mistakes (e.g. missing `AddWindowToolTipLayer` on the **main** window) if you still want to flag that — though that may duplicate “first hover” surprises; worth discussion.

---

## Testing suggestions (for the PR checklist)

- Hover a tooltip control, then **immediately** switch content / close window before the tooltip appears → **no** error log; no panic.
- Open a **modal** or Fyne **dialog** that contains tooltip widgets; verify tooltip still shows when fallback is implemented.
- Popup with `AddPopUpToolTipLayer` when main window **never** called `AddWindowToolTipLayer` → no log storm; popup tooltips may no-op (acceptable vs noisy logs).

---

## Reference implementation

We maintain a small vendored fork that implements the above for **Tanium Geminatus**; see the same directory’s **`UPSTREAM.md`** and diffs against **v0.4.0** in:

- `widget/tooltipwidget.go`
- `internal/tooltip_layer.go`
- `tooltip.go` (`AddPopUpToolTipLayer` nil guard)

You can diff this fork against the **v0.4.0** tag to extract a minimal patch for upstream.

---

## License / contribution note

fyne-tooltip is MIT-licensed; this proposal assumes the same license for any contributed patch.

---

*Draft prepared for discussion with the fyne-tooltip maintainers. Edit title/body as needed before opening the GitHub PR.*
