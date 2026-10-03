# file-baton

A Claude Code plugin for running several Claude sessions in the same git checkout.

- **One editor per file.** A session that edits a file holds its baton until its turn ends. Another session that tries to edit the same file is told who has it, what they're working on, and that it is in line.
- **Handoff with context.** When the holder finishes, the file goes to the next session in line together with the diff the holder made, the holder's task and any note it left.
- **Auto-resume.** A waiting session that has gone idle is woken up when the file reaches it, and carries on.
- **Commit guard.** A `git commit` that would sweep in another session's uncommitted changes is refused, with a suggestion for committing only your own files.
- **Crash recovery.** If a session's Claude process dies, its files are released at once, and its unfinished changes go to the next session with the handoff.

## How it looks

Session B tries to edit a file session A is changing:

```
file-baton: src/api.ts is being edited by another Claude session (3f2a91c0).
Their task: "add pagination to the list endpoint"
You are #1 in line. Do other parts of your task first, and do not work around this
by changing the file another way (for example with a shell command).
When they finish you will be given the file with a summary of their changes; if you
are idle by then you will be woken up automatically.
```

When A's turn ends, B receives:

```
file-baton: you now hold src/api.ts (you were waiting for it).
Session 3f2a91c0 finished with it. Their task: "add pagination to the list endpoint"
Their notes:
  - renamed listItems() to listPage(); callers updated
Their changes:
--- a/src/api.ts
+++ b/src/api.ts
@@ -10,6 +10,9 @@
...
Re-read the file before editing; your earlier view of it is out of date.
```

## Install

```bash
claude plugin marketplace add <owner>/file-baton
claude plugin install file-baton@file-baton
```

To try a local checkout without installing it:

```bash
make build
claude --plugin-dir /path/to/file-baton
```

## Commands

| Command | What it does |
|---|---|
| `/file-baton:status` | Files being edited, who is waiting, uncommitted changes per session, live sessions |
| `/file-baton:release <file>...` | Release files by hand (also `--mine`, `--all`); whoever waits gets them next |

Claude leaves handoff notes itself when someone is waiting, through `file-baton note`.
That command and `status` run without a permission prompt; `release` always asks.

## Settings

Set in `/config` (plugin options) or with environment variables, which take precedence.

| Option | Default | Environment variable |
|---|---|---|
| Auto-resume waiting sessions | on | `FILE_BATON_AUTO_RESUME` |
| Commit guard | on | `FILE_BATON_COMMIT_GUARD` |
| Handoff timeout: how long a handed-over file stays reserved while others wait | 10 min | `FILE_BATON_GRANT_TIMEOUT_MINUTES` |
| Idle release: free a session's files after this long without activity | 20 min | `FILE_BATON_IDLE_RELEASE_MINUTES` |
| Lines of diff in a handoff | 200 | `FILE_BATON_MAX_DIFF_LINES` |
| Turn file-baton off | off | `FILE_BATON_DISABLED=1` |

To commit everything despite the guard, prefix the command with `FILE_BATON_ALLOW=1`
(Claude is told to do this only when you explicitly ask).

## What it does not cover

- Edits made through shell commands (`sed -i`, redirects, code generators). Only Claude's edit tools are locked.
- Conflicts between different files, such as renaming a function in one file while another session calls it from another.
- `git commit` typed by a person in a terminal. The guard sees only Claude's commands.
- Sessions on different machines.
- Windows (macOS and Linux, amd64 and arm64, are supported).

If your sessions don't need to share a checkout, `claude --worktree <name>` gives each
one its own and avoids all of this.

## How it works

Every hook is a short run of a small Go binary. State lives in
`<git-common-dir>/file-baton/state.json`, shared by all worktrees of the repository and
changed only under an OS file lock, which the kernel releases if a process dies. Sessions
are identified by Claude Code's session id; liveness is the `CLAUDE_PID` process.

| Hook | Job |
|---|---|
| `PreToolUse` on Edit/Write/MultiEdit/NotebookEdit | take the file, queue for it, or deliver a handoff |
| `PreToolUse` on Bash | commit guard; approve file-baton's own `note`/`status` calls |
| `Stop` | ask for a handoff note if someone waits; release the turn's files |
| `Stop` (background, `asyncRewake`) | wake this session once when a file reaches it |
| `UserPromptSubmit` | remember the session's current task |
| `SessionStart` / `SessionEnd` | register / release everything |

Design and plans: [`docs/plans/`](docs/plans/).

## Troubleshooting

- `file-baton status --log` (or `/file-baton:status`) shows recent decisions. The log is in `.git/file-baton/log`.
- A file stuck with a session you closed: `/file-baton:release <file>`.
- Any internal error makes file-baton step aside (the edit goes through) and logs the error.

## Development

```bash
make test       # go vet + go test -race
make validate   # claude plugin validate --strict .
make build      # host binary into libexec/<os>-<arch>/
make dist       # all release binaries
make e2e        # two real Claude sessions racing for one file (costs a few cents)
```

Releasing: bump `version` in `.claude-plugin/plugin.json`, `make test validate dist`,
commit the binaries under `libexec/`, then `claude plugin tag --push`.
