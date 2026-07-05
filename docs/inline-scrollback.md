# Inline scrollback (conversation scrolling)

**Status: prototype, off by default.** Enable with `LUCINATE_INLINE=1`.

## The problem

The chat view renders into the terminal's **alternate screen buffer**
(`AppModel.View` sets `v.AltScreen = true`). The alt screen has no scrollback
and no native text selection, so the terminal hands the whole screen to the
app and every capability has to be re-implemented inside it. Three requirements
then collide:

- **Selection / copy** works only with mouse tracking **off** (issue #14) — but
  then the app never receives wheel events.
- With mouse off in the alt screen, terminals fall back to *alternate-scroll
  mode*: they translate the wheel into **arrow keys**. The native platforms'
  terminal does exactly this (it sends up/down key events when the display
  buffer is alternate); their swipe-to-scroll emits arrows too.
- Those arrows hit the chat key handler, where **Up/Down are reserved for
  message recall / history walk** and are deliberately not forwarded to the
  viewport. So a scroll gesture recalls a message instead of scrolling, and the
  history never moves.

In the *normal* buffer the native platforms' terminal already does the right
thing — native scrollback **and** native selection (its non-alternate scroll
path).

## The approach

Render the chat transcript **inline in the normal buffer** instead of the alt
screen — the same split Claude Code / Ink `<Static>` use. Finished turns are
pushed into the terminal's real scrollback with `tea.Printf`; only a small live
region (the in-flight turn + input) is redrawn in place. The terminal then owns
scrolling and selection, and Up/Down stay free for input history because the
scroll gesture never reaches the app.

Modal screens (pickers, config, connection list) stay on the alt screen;
`AppModel.View` sets `AltScreen = false` only for `viewChat` when inline. On
exit from a modal, Bubble Tea restores the normal buffer with the committed
transcript intact.

## The layout: a small live region, everything else in scrollback

The inline chat keeps the live region **small** — usually just the input box,
help line, and status bar (`inlineView`). Finished turns are spilled into the
terminal's own scrollback, so the live region flows naturally at the bottom of
the output and never fights the spill. The status bar is the **very last row**.

This is the load-bearing decision. A *full-height* live region (padding the
transcript up to fill the screen) was tried and abandoned: a large live region
conflicts with `insertAbove`-based scrollback spills, producing ghost frames and
blank gaps. A small region has room to spill above it and Just Works — this is
the Ink `<Static>` / Claude Code model.

`chatModel` splits its rows into **spilled** (already in scrollback, immutable)
and **live** (`m.messages[committedCount:]`, redrawn each frame).

- `historyLoadedMsg` → `commitLoadedHistory` spills the whole resumed history
  plus a resume divider. When the history fills the screen the input lands at the
  bottom with history above it; on a short session it floats where the content
  ends.
- **Finished turns stay live.** A completed turn is *not* spilled on its refresh
  — see "Why finished turns stay live" below. `reflowInline` (run from
  `AppModel.Update`) spills only the OLDEST turns, and only once the accumulated
  live tail would overflow the space above the input. So turns pile up in the
  live region and the oldest scroll off into scrollback as the screen fills.

## Why finished turns stay live

Committing a finished turn to scrollback *shrinks* the live region, and Bubble
Tea's inline renderer is top-anchored: it keeps the frame's top fixed and clears
the bottom, so the input jumps **up** by the turn's height and dead rows appear
at the bottom. (Verified in a real terminal; a resize doesn't re-anchor it.)

The fix is to never shrink the frame for a finished turn — keep it live. The
input then holds its row across the whole turn (send → stream → done), because
the live region only ever *grows*, and growing a bottom-seated inline frame just
scrolls the scrollback up while the input stays put. `reflowInline` spills the
oldest turns only when new content would overflow the screen; that spill happens
*during growth*, so it's balanced and the input doesn't move. A single response
taller than the screen is clipped to its newest rows for display (its top enters
scrollback once a later turn pushes it out of the newest slot).

Reconciliation still runs (`mergeHistoryRefresh`): finished turns are kept live
from the server-canonical merge, and `reflowInline` only ever spills the oldest
(already-canonical) rows, so the spilled prefix is append-only.

## Keeping the input steady during a turn

Because Bubble Tea's inline frame is **top-anchored**, the input (rendered below
the live tail) moves whenever the tail changes height — and a streaming turn
changes it constantly: the pre-response spinner appears then is dropped, tool
cards appear, new assistant segments start. Left alone the input jumps up and
down. (Ink/Claude Code avoids this by bottom-anchoring; Bubble Tea can't.)

`turnTailFloor` damps it. `updateInlineFloor` (run after every chat update)
records the high-water height of **everything above the input** —
`aboveInputBlock`: the live tail *plus* the completion menu, notifications, and
routine status. `inlineView` then holds that block at the floor via `fitAbove`
(top-padding) so it **never shrinks**. This covers both jitter sources:

- a streaming turn dropping a row (ephemeral tool card, dropped spinner), and
- the completion menu filtering down as you type `/age` (fewer matches → shorter
  menu → the input would otherwise rise).

The block may still grow the floor (input drifts down smoothly as text streams,
capped at the screen), but it never jitters upward. The floor resets to zero once
the block empties — the turn committed, the menu closed, we're idle — so the next
burst starts clean. Net: the input holds position during a turn or while
filtering, moving only when activity first appears and once when it clears.

A zero-size first frame (before the first `WindowSizeMsg`) is guarded in
`inlineView` — rendering at zero width would otherwise produce negative-width
styles and a corrupt frame.

## Clean first frame after the alt-screen transition

Entering inline chat from the agent picker (an alt-screen view) leaves Bubble
Tea's inline renderer with stale frame state: the first frame renders corrupt
(missing input border, ghosted status bar, split header) until the next resize.
The fix is in the `historyLoadedMsg` handler — it returns
`tea.Sequence(commitCmd, tea.RequestWindowSize)` so that, once the history has
been spilled to scrollback, a window-size round-trip fires. The renderer erases
and repaints on the resulting `WindowSizeMsg` (its `resize` always erases),
giving a clean first frame. The sequence matters: requesting the size in `Init`
instead races the history commit and doesn't stick.

## Frame-height safety

Every inline frame must be no taller than its line count implies, or the inline
renderer's cursor-up can't clear the previous frame (ghosting). Two rules:

- **No line may exceed the terminal width** — a wide line wraps to two rows.
  `clampToWidth` truncates every line, and the header/status and routine-status
  bars are truncated to `width − GetHorizontalFrameSize()` *before* lipgloss's
  `Width()` (which would otherwise wrap over-long content, padding included).
- **An over-tall in-flight turn is clipped** to its newest rows (`clipTail`) so
  the frame never exceeds the terminal height.

## Known limitations (prototype)

- **Input floats on short sessions** (empty space below). See below.

## Why not pin the input to the bottom

Tried twice, reverted twice — both failures trace to the same cause, so it's
worth stating plainly. The live region **must be free to change height**: the
completion menu, a multi-line composer, notifications, and the streaming turn all
grow and shrink it every keystroke. An inline live region grows *downward*, so it
needs empty rows below it to grow into. The float provides exactly that room.

Pin it to the bottom — either by padding the live region to full height, or by
padding scrollback to push it down — and that room is gone. The next time the
frame grows (typing `/` opens the menu), the terminal has to scroll, and Bubble
Tea's inline renderer can't clear the scrolled-away rows, ghosting the input and
status bar. A pinned, growable bottom prompt genuinely needs the alternate
screen — the thing we left to get native scroll and selection. On sessions with a
screenful of history the input sits at the bottom anyway; only fresh/short
sessions float, which matches ordinary terminal behaviour.
- **Scrolling fights active streaming.** Scrolling back while a turn streams gets
  interrupted (each delta redraws and terminals snap to the bottom). Scroll
  freely when idle.
- **Spill is asynchronous.** `tea.Printf` runs one loop tick after the watermark
  advances, so a just-finished turn can briefly flicker as it moves to scrollback.
- **Client-only rows don't reach scrollback.** `/help`, `/stats`, etc. render in
  the live region and vanish on the next refresh (the server never returns them).
- **Resize does not reflow** rows already in scrollback (expected; Claude Code
  has the same limitation).
- `/mouse` is unnecessary in inline mode (selection and scroll are native).

## Trying it

```
LUCINATE_INLINE=1 lucinate chat
```

Then check, in a real terminal (iTerm2, Terminal.app, or a native-platform
terminal): wheel/trackpad
scrolls the history; click-drag selects and copies; Up/Down still recall
previous input; slash-command pickers still open full-screen and return
cleanly.
