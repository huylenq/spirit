# project-code — progress

**Spec:** [project-code-design.md](project-code-design.md) — associate each project (dir basename) with an optional 3-char code, render as `[SPI]` prefix on session titles across spirit UIs. Opt-in per project.

## Status: all 3 phases built & verified. Awaiting user confirmation to crystallize + promote to established/.

## Done (built + verified)
- **Phase 1 — storage/lookup**
  - `internal/claude/session.go::ClaudeSession.ProjectCode` field.
  - `internal/claude/prefs.go`: `ProjectCodeKey`, `ProjectCodes() map[string]string` (scans LoadPrefs, strips `projectcode.` prefix), `SuggestProjectCode` + `splitProjectTokens` (acronym across `-_ .`/camelCase; single token → first 3 letters). Unit-verified: `spirit→SPI`, `lexicon→LEX`, `honeywell-forge-cognition-workspace→HFC`, `DanaAgent→DA`.
  - `internal/claude/discover.go`: `ProjectCodes()` loaded once per `DiscoverSessions`; `buildSession` takes `projectCodes` param and stamps `s.ProjectCode` after worktree resolution; **the phantom-Later constructor at `discover.go:190` also stamps** (was a bug — two spirit Later sessions came back empty until fixed).
  - Verified via `spirit eval`: with `projectcode.spirit=SPI` + `projectcode.notebooks=NBK` in prefs, all matching sessions (incl. phantom) carried the code; unassigned `honeywell-…` stayed empty.
- **Phase 2 — render** (two-tone colored, UI-layer segment)
  - `internal/ui/project_code.go`: `renderProjectCode(s)` / `renderProjectCodeBg(s, bg)` / `projectCodeWidth(s)`. Brackets `ColorFaint` (`#4b5563`, new in styles.go), code `ColorMuted` (`#9ca3af`) — brackets fainter. `claude.Titled` was **removed** (styling is a UI concern, and titles flow through ANSI-hostile paths).
  - Prefix is a **separate styled segment** prepended outside each name's styling; each site keeps a plain name through width-math/highlight/truncation and reserves `projectCodeWidth(s)`: `session_item.go` (renderProjectCodeBg for selected rows, renderProjectCode otherwise), `sidebar.go` claudingEntry (new `ClaudingEntry.CodePrefix` field, rendered static ahead of the shimmering name in `allquiet.go`), `detail_view.go`, `workqueue.go`.
  - Verified: `spirit capture` (ANSI-stripped) shows `[SPI] add project code`, `[NBK] oracle annotation` with correct alignment/no truncation drift; a profile-forced unit test confirmed brackets and code emit **distinct** SGR codes (`\x1b[90m` vs `\x1b[37m`).
- **Phase 3 — assignment UX**
  - `StateProjectCodeEdit` + `m.projectCodeRelay`/`m.projectCodeProject` (model.go); `ui.NewProjectCodeRelayModel` (relay.go, `▣ ` prompt, CharLimit 3).
  - `execProjectCodeEdit` (command_relay.go) — prefills existing code or `SuggestProjectCode`; `handleKeyProjectCodeEdit` (update_relay.go) — sanitizes to ≤3 uppercase alnum, Enter saves via `saveProjectCode` (app/prefs.go, deletes key on blank), Esc cancels; optimistic update of `m.sessions`.
  - Keybinding **`alt+p`** (keymap.go `ProjectCode`); dispatch wired in update.go + update_normal.go. The input field renders in the **footer bar** (`renderFooter` case in view_overlays.go: `projectCodeRelay.View()` + `project · enter set · esc cancel · blank clears`), NOT inline in the detail prompt — no `SetRelayView` for this state.
  - `project_code` exposed on Lua session table (convert.go) for eval/verification.

## Decisions made mid-build (fold into spec on promotion)
- **Suggestion is first-3-letters for single tokens** → `spirit`→`SPI` (not the spec's aspirational `SPR`). No consonant-skeleton heuristic — keep it predictable; user edits if they want. Spec example already corrected.
- **Phantom-Later sessions must be stamped too** (`discover.go:190`) — a second constructor the spec's Decision 3 didn't call out. Fold into Decision 3.
- **Detail header prefixes the title even though it already shows the full project name** — chosen for rule uniformity (`[SPI]` chip is visually distinct from the project word). Mild redundancy accepted; revisit if it reads noisy.
- **Keybinding = `alt+p`** (free; mnemonic "project"; avoids ctrl+a tmux prefix).
- **Storage = namespaced prefs keys** `projectcode.<project>` (as designed). Note: `savePrefs` rewrites the whole file, so concurrent writers race — pre-existing behavior, not introduced here.
- **Two-tone prefix, rendered as a separate UI segment** (not inside the title string). Brackets `ColorFaint`, code `ColorMuted`. Load-bearing reason: titles pass through `highlightMatch` (raw-rune fuzzy match), lipgloss `bg.Render`/`ItemDetailStyle.Render` wraps, and `shimmer` (per-rune) — all corrupt or strip embedded SGR. Each site reserves `projectCodeWidth(s)` and keeps the name plain. Folds into spec Decision 4 (revised in place).

## Next
- Manual smoke in a live TUI: `alt+p` on a session → editor prefilled with suggestion → accept/edit → `[CODE]` appears next poll; blank → cleared. (Headless render + eval already confirm the data path; only the interactive key path is unexercised.)
- On user confirmation: `lexicon:crystallize` the new vocab (`«project»`, `«project-code»`, `«display-name»`, `Titled`), then promote Shape A → `established/project-code.md`, delete this note.

## Gotchas
- `make` (not `make build`) after changes — restarts daemon. Codes are re-read from prefs each poll (~1s), so assignments appear within a poll.
- Lua doesn't expose `is_phantom`/`later_id` → they read `nil` in eval; don't infer phantom-ness from that.
