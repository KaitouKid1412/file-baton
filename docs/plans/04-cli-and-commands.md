# Plan 04: CLI, slash commands and config

## CLI (`file-baton <cmd>`)

Run by Claude through Bash as `"<plugin root>/libexec/file-baton" <cmd>`; every hook
message that mentions the CLI prints that absolute path. The session is identified by
`CLAUDE_CODE_SESSION_ID` (present in the Bash tool's environment); `--session` overrides.

| Command | Behaviour |
|---|---|
| `note <file> <text…>` | Append a note to the lock the caller holds on `<file>` (max 5 notes, 1000 chars each). Not holding it → exit 1 with a clear message. |
| `note --all <text…>` | Same note on every lock the caller holds that has waiters (or all held, if none have). |
| `status [--json]` | Locks (holder, task, age, queue), pending grants, uncommitted changes per session, live sessions. Prunes touches first. |
| `release <file>…` / `release --all` / `release --mine` | Manual override. Releases the named locks whoever holds them, with reason `forced` and a normal handoff to the next waiter. `--mine` uses the calling session. |
| `version` | Version and build info. |
| `hook <event>` | Hook entry points (plans 01–03). |

Exit codes: 0 ok, 1 usage or state error, 2 reserved for the waiter's rewake.

### Auto-allow for its own calls

`pre-bash` returns `permissionDecision: "allow"` for a command that is exactly one
invocation of this plugin's launcher (the absolute path, quoted or not) with `note` or
`status`, and contains none of `` ; & | ` $( > < `` or a newline. Anything else gets no
decision and goes through the normal permission flow. `release` is never auto-allowed.

## Slash commands (plugin skills)

`skills/status/SKILL.md`:
```markdown
---
name: status
description: Show which files Claude sessions in this repo are holding, waiting for, or have uncommitted changes in.
---
!`"${CLAUDE_PLUGIN_ROOT}/libexec/file-baton" status`

Show the status above to the user as is, then add one line on anything that needs attention (a long-held lock, a crashed session's uncommitted changes).
```

`skills/release/SKILL.md`: `disable-model-invocation: true`, `argument-hint: <file>... | --all | --mine`,
body runs `release $ARGUMENTS` and reports the result. Only the user triggers it.

## Config (`internal/config`)

Order (later wins): defaults → `CLAUDE_PLUGIN_OPTION_<KEY>` (plugin userConfig) → `FILE_BATON_<KEY>` env.

| Key | Type | Default | userConfig |
|---|---|---|---|
| `auto_resume` | bool | true | yes |
| `commit_guard` | bool | true | yes |
| `grant_timeout_minutes` | number | 10 | yes |
| `idle_release_minutes` | number | 20 | yes |
| `max_diff_lines` | number | 200 | no |
| `disabled` | bool | false | no (env escape hatch) |

Invalid values fall back to the default and are logged.

## Logging

Append-only `<common>/file-baton/log` (rotated at 1 MiB, one old file kept): every
decision, error and fail-open, one line each. `status --log` tails it.

## Tasks

- [ ] `internal/config`
- [ ] `internal/cli`: note, status (text + json), release, version
- [ ] Auto-allow matcher + tests (quoting, metacharacters, other commands)
- [ ] Skills `status` and `release`
- [ ] Log file with rotation

## Done when

- `note` from a Bash call in a real session attaches to the right lock without a permission prompt.
- `/file-baton:status` lists locks, queues and uncommitted changes.
- `/file-baton:release --all` frees everything and wakes the next waiters.
