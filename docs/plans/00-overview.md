# file-baton: overview and decisions

file-baton is a Claude Code plugin that lets several sessions work in one checkout
without overwriting each other. A session that edits a file holds that file's baton
(lock) until its turn ends. Another session that wants the file joins a queue, and when
the baton is passed on it arrives with context: what changed, why, and any note the
previous holder left.

## Decisions

| Topic | Decision |
|---|---|
| Name | `file-baton` (`baton` is used by 8+ Claude Code projects) |
| Language | Go, single static binary, standard library only |
| Integration | Classic command hooks (stable API), exec form, `${CLAUDE_PLUGIN_ROOT}` |
| Platforms (v1) | macOS and Linux, amd64 + arm64. Windows later |
| Scope (v1) | Git repositories only; outside a repo every hook is a no-op |
| Failure policy | Fail open: an internal error never blocks Claude, it logs and allows |

### v1 features

1. Automatic locks on Claude's edit tools (`Edit`, `Write`, `MultiEdit`, `NotebookEdit`), held until the end of the session's turn.
2. FIFO queue; a blocked edit is denied with who holds the file, their task and the queue position.
3. Handoff: on release the baton is reserved for the head of the queue together with the diff the holder made, the holder's task and notes.
4. Auto-resume (on by default): an idle waiting session is woken when the baton reaches it.
5. Crash recovery: a session whose Claude process is gone loses its locks immediately.
6. Commit guard: a `git commit` that would include another session's uncommitted changes is denied.
7. Handoff notes: `file-baton note <file> "<text>"`, prompted for when someone is waiting.
8. `/file-baton:status` and `/file-baton:release` slash commands.

Not in v1: edits made through Bash (`sed -i`, redirects), coordination across machines,
human `git commit` in a terminal, Windows.

## Facts verified on Claude Code 2.1.287 (spike, see 01-core.md)

- Hook processes are direct children of the Claude process; `CLAUDE_PID` is exported to them.
- The Bash tool's environment carries `CLAUDE_CODE_SESSION_ID` and `CLAUDE_PID`, so the CLI knows which session runs it.
- Hook stdin carries `session_id`, `cwd`, `transcript_path`, `tool_name`, `tool_input`; `agent_id` inside subagents; Stop carries `stop_hook_active`.
- PreToolUse can return `permissionDecision` `allow`/`deny` with a reason, and `updatedInput`.
- A Stop hook with `asyncRewake: true` that exits 2 wakes Claude with its stderr. **It runs again after every Stop**, so a waiter that exits 2 without consuming its trigger loops forever.
- `${CLAUDE_PLUGIN_ROOT}` is substituted in skill bodies, including `` !`cmd` `` injections.
- userConfig values reach hooks as `CLAUDE_PLUGIN_OPTION_<KEY>`.
- A top-level `bin/` makes claude.ai/Cowork refuse the plugin, so executables live in `libexec/`.

## Architecture

```
Claude Code session ──hook JSON on stdin──▶ libexec/file-baton (sh launcher)
                                              └─▶ libexec/<os>-<arch>/file-baton (Go)
                                                     │ flock
                                                     ▼
                              <git-common-dir>/file-baton/state.json   (+ snapshots/)
```

Each hook invocation is a short-lived process: resolve the repo from `cwd`, take an
exclusive `flock` on `state.lock`, load `state.json`, reap dead sessions, apply the event,
write `state.json` atomically (temp + rename), unlock, print the hook's JSON answer.
`flock` is released by the kernel if the process dies, so there are no stale mutexes.

The store lives under `git rev-parse --git-common-dir`, so all worktrees of a repo share
it. Locks are keyed by absolute, symlink-resolved path, so the same relative path in two
worktrees never conflicts.

### Code layout

```
cmd/file-baton/        main: subcommand dispatch
internal/state/        types, load/save, flock
internal/engine/       pure logic over State: acquire, release, reap, grant, commit check
internal/hook/         stdin parsing, output builders, one handler per event
internal/gitx/         repo discovery, snapshot diff, status, commit-set calculation
internal/proc/         process liveness
internal/config/       defaults <- CLAUDE_PLUGIN_OPTION_* <- FILE_BATON_* env
internal/cli/          note, status, release subcommands
libexec/file-baton     POSIX sh launcher picking the platform binary
hooks/hooks.json       hook wiring
skills/status, skills/release
```

`engine` takes the state, a clock and a liveness function, so every rule is unit-tested
without processes, git or the filesystem.

## Hook map

| Event | Command | Purpose |
|---|---|---|
| SessionStart | `hook session-start` | register session (pid, transcript path) |
| UserPromptSubmit | `hook prompt` | record the current task |
| PreToolUse `Edit\|Write\|MultiEdit\|NotebookEdit` | `hook pre-edit` | acquire / queue / deny / deliver handoff |
| PreToolUse `Bash` | `hook pre-bash` | commit guard; auto-allow file-baton CLI calls |
| Stop (sync) | `hook stop` | ask for notes if someone waits; release; hand off |
| Stop (`asyncRewake`) | `hook wait` | wake this session once when a baton reaches it |
| SessionEnd | `hook session-end` | release everything, leave queues |

## Plans

1. `01-core.md`: state store, sessions and liveness, edit locking, release at end of turn.
2. `02-handoff.md`: diffs, reservations, delivery, auto-resume, holder notification.
3. `03-commit-guard.md`: tracking uncommitted changes per session, commit interception.
4. `04-cli-and-commands.md`: note/status/release CLI, slash commands, config.
5. `05-packaging.md`: launcher, manifests, build, tests, end-to-end run, release.

Each phase ends with `go test ./...` green and `claude plugin validate .` passing.

## Status (v0.1.0, 2026-10-02)

All five plans are implemented. `make test` and `make validate` pass, and `make e2e`
passed with two real Haiku sessions: B was blocked, went idle, was woken 0.5 s after A
finished, and received A's task, note and diff.

Changes from the plans, found while building:

- The "notes asked" flag lives on the lock, not the session. `stop_hook_active` is true after an asyncRewake wake-up, so it can't be used to avoid asking twice.
- Skill shell injections need permission like any Bash call, so both skills declare `allowed-tools` for their exact command (`${CLAUDE_PLUGIN_ROOT}` is substituted there).
- Handoff and notes messages say explicitly that files are released automatically. Without that, sessions in the e2e run went looking for a release or finish command.
- Claude Code refuses bare `sleep`, so the e2e script holds the file with a small `slow-build.sh`.
