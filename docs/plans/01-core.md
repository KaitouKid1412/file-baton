# Plan 01: core locking

Goal: two sessions in one repo can never have the same file's baton at once; a session
holds batons only during its turn; crashed sessions lose theirs.

## State (`<git-common-dir>/file-baton/state.json`, schema version 1)

```go
type State struct {
    Version  int                  `json:"version"`
    Sessions map[string]*Session  `json:"sessions"`  // by session_id
    Locks    map[string]*Lock     `json:"locks"`     // by absolute real path
    Touched  map[string][]Touch   `json:"touched"`   // by absolute real path (plan 03)
}

type Session struct {
    ID         string    `json:"id"`
    PID        int       `json:"pid"`              // CLAUDE_PID, 0 if unknown
    Transcript string    `json:"transcript"`       // transcript_path, activity signal
    Task       string    `json:"task"`             // latest prompt, one line, <= 300 chars
    StartedAt  time.Time `json:"started_at"`
    LastSeen   time.Time `json:"last_seen"`        // any hook from this session
    WaiterPID  int       `json:"waiter_pid"`       // plan 02
}

type Lock struct {
    Path       string    `json:"path"`
    Owner      string    `json:"owner"`            // session id
    Status     string    `json:"status"`           // "held" | "granted"
    Since      time.Time `json:"since"`
    Snapshot   string    `json:"snapshot"`         // file name under snapshots/, "" = file did not exist
    Queue      []Waiter  `json:"queue"`
    Notes      []string  `json:"notes"`            // plan 04
    Handoff    *Handoff  `json:"handoff,omitempty"` // plan 02, set while Status == "granted"
    Told       []string  `json:"told,omitempty"`   // waiters the holder was told about (plan 02)
    NotesAsked bool      `json:"notes_asked"`      // plan 02, asked once per holding
}

type Waiter struct {
    Session string    `json:"session"`
    Since   time.Time `json:"since"`
}
```

Unknown `version` → treat as empty state and log (never crash, never block).

## Store mechanics (`internal/state`)

- `Open(cwd)`: `git -C cwd rev-parse --show-toplevel --git-common-dir`; not a repo → `ErrNoRepo` (callers exit 0 silently).
- Directory `<common>/file-baton/` created on demand (0700). Files: `state.lock`, `state.json`, `snapshots/`.
- `Update(fn func(*State) error)`: `flock(LOCK_EX|LOCK_NB)` polled every 10 ms for up to 3 s (then fail open), read, `fn`, write `state.json.tmp` + `rename`, unlock.
- `View(fn)` for read-only callers takes the same lock (state is tiny; simplicity over a shared lock).

## Liveness (`internal/proc`, used by `engine.Reap`)

`alive(s)`:
1. `s.PID > 0`: `kill(pid, 0)` succeeds or fails with `EPERM` → alive; `ESRCH` → dead.
2. `s.PID == 0` (env missing): alive while `now - max(LastSeen, mtime(Transcript)) < idle_release`.

Turn-level idleness: Stop does not fire when the user interrupts a turn with Esc, so a
held lock could outlive its turn. `Reap` also releases `held` locks whose owner shows no
activity (`max(LastSeen, transcript mtime)`) for `idle_release` (default 20 min). The
transcript is appended on every message and tool result, so this costs no extra hooks.

## Engine rules (`internal/engine`)

`Reap(st, now, alive)`, run first in every `Update`:
- dead session → each `held` lock: release with reason `crashed` (plan 02 handoff); each `granted` lock: pass to next waiter; remove it from every queue; delete session.
- idle owner (above) → release its `held` locks with reason `idle`.
- `granted` lock older than `grant_timeout` (default 10 min) and not delivered → pass to next waiter or delete.
- drop waiters whose session no longer exists.

`Acquire(st, sid, path, now) Decision`:
| Lock state | Result |
|---|---|
| none | create `held` for sid, take snapshot → **allow** |
| held/granted by sid, no pending handoff | **allow** |
| granted to sid with undelivered handoff | deliver (plan 02) → **deny once** with handoff text |
| held/granted by other | enqueue sid if absent → **deny** with holder, task, position |

`EndTurn(st, sid)` (Stop): for every `held` lock of sid: record touch if changed (plan 03),
then hand off to the first live waiter (plan 02) or delete; delete its snapshot. `granted`
locks of sid are kept (the waiter will deliver them). Queue entries of sid are kept.

`EndSession(st, sid)` (SessionEnd): like `EndTurn`, plus pass on `granted` locks, remove
sid from all queues, delete the session. Must finish well inside the 1.5 s SessionEnd budget.

## Hooks in this phase

- `session-start`: upsert session `{pid: $CLAUDE_PID, transcript}`. No output.
- `prompt`: upsert, set `Task` (whitespace collapsed, 300 chars).
- `pre-edit`: path from `tool_input.file_path` (`notebook_path` for NotebookEdit); relative → join with `cwd`; resolve symlinks (parent dir if the file does not exist). Outside the worktree root → no output (allow). Else `Acquire`.
- `stop`: `EndTurn` (notes request comes in plan 02).
- `session-end`: `EndSession`.

Subagents carry the parent's `session_id`, so they share the session's batons.

## Deny message (pre-edit, file held by another session)

```
file-baton: src/api.ts is being edited by another Claude session (3f2a91c0).
Their task: "add pagination to the list endpoint"
You are #1 in line. Do other parts of your task first; do not work around this
by editing the file another way. You will get the file with a summary of their
changes when they finish.
```
(The last sentence becomes "Retry this edit later." when auto-resume is off.)

## Tasks

- [ ] `internal/state`: types, `Open`, `Update`, `View`, atomic write, flock with timeout
- [ ] `internal/proc`: `Alive(pid)`
- [ ] `internal/engine`: `Reap`, `Acquire`, `EndTurn`, `EndSession` + table tests
- [ ] `internal/hook`: input struct, output helpers (`allow`, `deny(reason)`, `context`), dispatcher
- [ ] `cmd/file-baton`: `hook <event>` subcommand; read stdin, run, always exit 0 on internal error
- [ ] Integration test: temp repo, two fake sessions (alive pid = test process, dead pid = exited child) driving the binary through stdin

## Done when

- Session B is denied while A holds a file, allowed after A's Stop.
- Killing A's process frees its locks on B's next attempt.
- Non-repo directories and paths outside the worktree are untouched.
