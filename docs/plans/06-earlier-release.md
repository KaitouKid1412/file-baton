# Plan 06: earlier release (v0.2.0)

Goal: a session that works through one long turn (an hour or more, common for background
sessions given a whole plan) must not hold every file it touched until the turn ends.

## 1. Release on commit

A commit is the clearest "done with this for now" signal a session gives.

- New hook: `PostToolUse` on `Bash` → `hook post-bash`.
- When the command contains a `git commit` (same parser as the commit guard, override
  prefix included), release every lock the session **holds** whose file is now clean in
  git (`git status --porcelain -- <file>` empty). Files the commit did not include, or
  that still have uncommitted changes, stay held. A failed commit leaves files dirty, so
  it releases nothing; no need to read the commit's output.
- Reason `committed`; the handoff says "Session X committed its changes to it" and still
  carries the diff since the holder took the file, its task and notes.
- No uncommitted-change record is written (the file is clean), so the commit guard stays quiet.
- If a released file went to a waiting session, the committer gets one line of context:
  it no longer holds the file and editing it again may mean waiting.

## 2. Per-file hold limit

- `Lock.LastEdit`: set when the lock is taken and on every allowed edit by its owner.
  Locks saved by v0.1 have no `LastEdit`; `Since` stands in for it.
- `Reap` releases a held lock whose `LastEdit` is older than the hold limit, reason
  `expired`, even while the owner's turn goes on. The owner editing the file again later
  takes it again if free, or queues like anyone else.
- Setting: `FILE_BATON_HOLD_MINUTES`, default 10, `0` turns the limit off.
- Order of checks in `Reap`: dead session, then idle session (20 min, wording "went quiet"),
  then hold limit, then grant timeout.

## Tasks

- [x] `config`: `HoldTimeout` (10 min, 0 = off)
- [x] `state.Lock.LastEdit`; `engine`: set it, expire in `Reap`, `ReleaseCommitted(sid)`
- [x] Handoff wording for `committed` and `expired`
- [x] `hook post-bash` + hooks.json entry
- [x] Tests: unit (committed clean vs dirty, expiry, 0 = off, v0.1 lock without LastEdit), integration (real git commit hands the file on; tiny hold limit hands it on mid-turn)
- [x] README settings and release table; overview status
- [x] Release v0.2.0
