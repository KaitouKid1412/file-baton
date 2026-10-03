# Plan 02: handoff and auto-resume

Goal: when a baton moves from one session to the next, the receiver learns what changed
and why, and an idle receiver resumes on its own.

## Snapshot and diff

- On acquire, copy the file's current bytes to `snapshots/<sha1(path)>-<sid8>`; missing file → `Snapshot = ""`.
- On release, diff snapshot vs current with `git diff --no-index --no-color -U3 <snap|/dev/null> <path>`, relabel headers to the repo-relative path, cap at `max_diff_lines` (default 200) with a `… N more lines` marker. Empty diff → "no changes".
- Binary or > 1 MiB files: summary line only ("binary file changed", "+N/-M lines").

## Handoff record

```go
type Handoff struct {
    From      string    `json:"from"`       // session id
    FromTask  string    `json:"from_task"`
    Reason    string    `json:"reason"`     // released | crashed | idle | ended | forced
    Notes     []string  `json:"notes"`
    Diff      string    `json:"diff"`
    At        time.Time `json:"at"`
    Delivered bool      `json:"delivered"`
}
```

`release(lock, reason)`: build the handoff from the lock's snapshot, notes and the owner's
task; pop the first waiter whose session is alive. If one exists, the lock becomes
`granted` to it with the handoff; otherwise the lock is deleted.

## Delivery: exactly once

A handoff is delivered by whichever happens first:

1. **Waiter (auto-resume).** `hook wait` (Stop, `asyncRewake: true`, timeout 3600 s):
   - exit 0 at once if auto-resume is off, or the session has no queue entry and no undelivered grant;
   - record `WaiterPID = getpid()` (a newer waiter supersedes an older one, which then exits 0);
   - poll every second under the lock: reap; if a lock is `granted` to this session with an undelivered handoff, mark it delivered, switch it to `held` with a fresh snapshot, print the handoff text to stderr and **exit 2**; if the session is no longer queued anywhere, exit 0; stop at 3500 s.
   - Because delivery is marked under the lock before exiting 2, the next Stop's waiter finds nothing to deliver and exits 0. That breaks the loop seen in the spike.
2. **Next edit attempt.** `pre-edit` on a `granted` lock with an undelivered handoff: mark delivered, switch to `held`, re-snapshot, **deny once** with the handoff text (Claude must re-read the file anyway). The following attempt is allowed.

## Handoff text

```
file-baton: you now hold src/api.ts (you were waiting for it).
Session 3f2a91c0 finished with it. Their task: "add pagination to the list endpoint"
Their notes:
  - renamed listItems() to listPage(); callers updated
Their changes:
  @@ -10,6 +10,9 @@ ...
Re-read the file before editing; your earlier view of it is out of date.
Files are released automatically when your turn ends; there is no command to release or finish them.
```
Reason `crashed`/`idle` replaces the second line with "Session 3f2a91c0 stopped
responding; its unfinished changes are below."

## Telling the holder someone is waiting

- **Heads-up.** When the holder next runs `pre-edit` or `pre-bash` and a waiter is not yet in `Told`, add `additionalContext`: "file-baton: session X is waiting for src/api.ts. Before you finish, leave a short note on what you changed and why: `<cli> note src/api.ts "<note>"`." Add X to `Told`.
- **At Stop: removed in v0.1.2.** v0.1.0 blocked the holder's Stop once to ask for notes. Claude Code shows a blocking Stop as "Stop hook error" in the holder's terminal, and Claude answered it with an extra reply, usually "no note needed". The diff and task already reach the waiter, so the heads-up during the turn is the only invitation now.

## Config used here

`auto_resume` (bool, true), `grant_timeout` (10 min), `max_diff_lines` (200).

## Tasks

- [ ] `gitx.SnapshotDiff(snapshot, path, rel, maxLines)`
- [ ] Snapshot write/delete helpers in `state`
- [ ] `engine.release`, grant to next live waiter, `Handoff` construction
- [ ] Delivery in `Acquire` (deny once) and in `hook wait` (exit 2)
- [ ] `WaiterPID` supersession; poll loop with deadline
- [ ] Heads-up context and Stop notes request
- [ ] Tests: handoff content, delivery exactly once across both paths, waiter supersession, crashed holder handoff, grant timeout passes to the next waiter

## Done when

- A's turn ends → idle B wakes within ~1 s with A's diff and notes, edits, ends its turn → C (queued behind B) wakes next.
- No session is woken twice for one handoff.
