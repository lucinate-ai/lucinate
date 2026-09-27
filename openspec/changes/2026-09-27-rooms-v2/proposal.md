# Rooms v2 — routing, streaming, resume, export, compaction, member UX

## Why

A hosted room answers with **every** member: a six-profile room costs six turns for one
question, and a user who wants one answer has to type a handle every time. The transcript also
reads poorly for long conversations (unbounded history, no way to search it), a dropped socket
loses the view's context, and nothing leaves the client — there is no way to hand a room's
transcript to anyone else.

This change adds the six capabilities the room surface was missing, all client-side: the hosted
room protocol (transcript, roster, server-side mention routing) is unchanged.

## What changes

- **Routing** — `@mention` parsing with the gateway's grammar, plus per-room `moderator` and
  `round-robin` modes that add the mention themselves, because mention routing is server-side.
- **Streaming** — member replies render as they are written (braille spinner at 120 ms, the same
  cadence the chat view uses), and a reply that ends empty removes its placeholder.
- **Resume** — a dropped socket redials on exponential backoff (500 ms → 30 s) and refetches the
  log from the view's cursor, so nothing on screen is lost.
- **Export** — `/export [md|json|both]` writes the transcript into the lucinate data dir.
- **Compaction** — `/compact [N]` asks the room for a brief over everything older than the last
  `N` messages and renders it in their place; `/compact local [N]` builds a deterministic local
  brief with no roster reply. The gateway log is never modified.
- **Member UX** — per-member header colours (persisted), `/cost` tokens and cost per member,
  `/find <phrase>` transcript search with highlighting.

## Compatibility

Existing behaviour is preserved: a room with no preferences broadcasts exactly as before, the
plain transcript rendering is unchanged, and every new command is additive. The room's local
settings live in a new file (`rooms-prefs.json`) so a bad write cannot cost the user their
predefined rooms (`rooms.json`).

## Risks

- The gateway's streaming vocabulary is not documented in this repository, so the client accepts
  both a dedicated delta kind and a partially-marked `message.member`; a gateway that emits
  neither degrades to poll-only rendering (the turn rows still show, without partial text).
- `/compact` posts one request into the room, which the roster answers — it costs one turn set.
  `/compact local` exists precisely so that cost is opt-in.
