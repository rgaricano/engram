# Edit observation in the TUI

## Objective
Add an `e edit` shortcut to the observation detail panel (next to
`j/k scroll • c copy • t timeline • esc back`) that opens an editor for the
observation **title and content**, with save on `ctrl+s` and cancel on
`esc`/`q`.

## Problem
The TUI detail view (internal/tui) is read-only: users can copy or view the
timeline but cannot correct an observation from the TUI. The store already
supports updates (`UpdateObservation`), so only the UI layer is missing.

## Why
User request. Design decision made with the user: save with `ctrl+s` instead
of `enter`, so `enter` keeps its native editor meaning (newline in content).

## Scope
In scope:
- New screen `ScreenObservationEdit` with title input + content textarea.
- Keybindings: `enter` (newline in content / move to content from title),
  `ctrl+s` save, `esc`/`q` cancel, `j`/`k`/`tab`/`up`/`down`/`shift+tab`
  focus movement and content scrolling.
- Help line update in the detail panel.
- Deterministic tests (update, view, model layers).

Out of scope:
- Metadata editing (`type`, `scope`, `topic_key`) — store supports it, TUI
  does not in this feature.
- Push/PR — remain human decisions.

## Constraints
- Follow the existing Bubbletea patterns (skills: gentleman-bubbletea,
  tui-quality): Screen constant in model.go, all state in Model, per-screen
  key handler returning (tea.Model, tea.Cmd), per-screen view case.
- Reuse store API: `UpdateObservation(id, UpdateObservationParams{Title,
  Content})` (internal/store/store.go:5312-5320). Do not add new store methods.
- Reuse existing components: `textInput` for the title, `bubbles/textarea`
  for the content (already in go.mod).
- Store validation (non-empty title/content) and version capture on content
  change must remain the source of truth; the TUI surfaces errors via ErrorMsg.

## Tasks
- [ ] T1: Screen constant + Model state + save command (model.go)
  - `ScreenObservationEdit` after `ScreenObservationDetail`.
  - Fields: `EditTitle textInput`, `EditContent textarea.Model`,
    `EditError string`, `EditFeedback string`.
  - `observationUpdatedMsg{observation *store.Observation; err error}`.
  - `saveObservationEdit(s *store.Store, id int64, title, content string) tea.Cmd`
    calling `s.UpdateObservation(id, store.UpdateObservationParams{Title: &title, Content: &content})`.
- [ ] T2: Key handling and flow (update.go)
  - `e` case in `handleObservationDetailKeys`: initialize buffers from
    `SelectedObservation` (title via SetValue, content via SetValue,
    Width = m.Width - 4, Height = available content rows,
    CharLimit = s.MaxObservationLength(), KeyMap.LineNext = ("down","j","tab")),
    PrevScreen = ScreenObservationDetail, Screen = ScreenObservationEdit,
    focus content.
  - Focused-input dispatch in `Update()` before `handleKeyPress` (pattern:
    `handleSearchInputKeys` / `handleCloudConfigInputKeys`).
  - `handleObservationEditKeys(msg tea.KeyMsg)`:
    - `ctrl+s` -> saveObservationEdit with current buffer values.
    - `esc`/`q` -> back to PrevScreen, DetailScroll = 0, reload detail.
    - `tab`/`j`/`down`: title focused -> focus content; content focused ->
      delegate to textarea (scroll down).
    - `shift+tab`/`k`/`up`: content focused -> focus title; title focused ->
      no-op.
    - `enter`: title focused -> focus content; content focused -> delegate
      to textarea (newline).
    - Everything else -> delegate to the focused buffer.
  - `observationUpdatedMsg` handler: success -> update SelectedObservation,
    EditFeedback = "✓ Saved!", schedule clear; error -> ErrorMsg, stay in edit
    screen.
- [ ] T3: Rendering (view.go, styles.go)
  - `viewObservationEdit()`: header "Observation #N — Edit", title row with
    EditTitle.View(), separator, EditContent.View(), line counter, help
    "j/k navigate • ctrl+s save • esc cancel".
  - `View()` case `ScreenObservationEdit`.
  - `observationDetailHelp()` -> "j/k scroll • c copy • t timeline • e edit • esc back".
  - `EditFeedback` rendered in View() like CopyFeedback.
- [ ] T4: Deterministic tests
  - update_test.go: `e` opens edit with buffers initialized and content
    focused; `esc` cancels without store write; `ctrl+s` emits
    saveObservationEdit with current values; `enter`/`j`/`k`/`up`/`tab`
    focus transitions.
  - view_test.go: edit screen renders header, title input, content, help
    line; detail help now contains "e edit".
  - model_test.go: observationUpdatedMsg handler success (SelectedObservation
    updated, feedback) and error (ErrorMsg, screen unchanged).

## Acceptance criteria
- From any observation detail, `e` opens the editor pre-filled with title +
  content.
- Editing title/content and `ctrl+s` persists via the store; the detail view
  shows the updated values; a content change creates a timeline version.
- `esc`/`q` discards edits; invalid edits (empty title/content) show a clear
  error without persisting.
- `go build ./...` and `go test ./internal/tui/...` pass.
- Manual: `engram tui` -> open an observation -> edit -> save -> verify.

## Checks
- `go build ./...`
- `go test ./internal/tui/...`
- Manual `engram tui` check of the editor flow.
- `gentle_review` inspect before declaring the work-unit complete.

## Progress
- Status: planned (2026-06-18). Branch: pending (default branch is main).
- Route: delegated direct (multi-file write trigger: 2+ non-trivial files).
- Evidence: exploration in parent session; store API at store.go:5312-5320,
  textarea in go.mod, detail screen at view.go:416 / update.go handleObservationDetailKeys.

## Next step
Create feature branch, launch bounded writer for T1-T4 with this document,
then parent verification (T5: build, test suite, manual check, commit).
