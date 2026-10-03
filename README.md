# file-baton

Run several Claude Code sessions in the same git checkout without them stepping on each other.

- **One editor per file.** A session that edits a file holds its baton until its turn ends. Another session that tries to edit it is told who has it and waits in line.
- **Handoff with context.** When the holder finishes, the next session gets the file together with the holder's diff, task and notes.
- **Auto-resume.** A waiting session that went idle wakes up by itself when the file reaches it.
- **Commit guard.** A `git commit` that would sweep in another session's uncommitted work is refused.
- **Crash recovery.** If a session dies, its files are released at once.

Nothing to configure, no daemon, no account.

## Requirements

- Claude Code (tested with 2.1.287)
- A git repository: file-baton stays out of the way everywhere else
- macOS or Linux (amd64 or arm64)

## Install

```bash
claude plugin marketplace add <owner>/file-baton
claude plugin install file-baton@file-baton
```

Or from inside Claude Code: `/plugin install file-baton --marketplace <owner>/file-baton`.

Sessions that were already open pick it up after `/reload-plugins` or a restart.

## Try it in two minutes

1. Open two terminals in the same git repository and start `claude` in both.
2. In the first, ask for a change that takes a little while, for example:
   `Add input validation to every function in utils.py, then run the tests.`
3. While it works, ask the second for a different change to the same file:
   `Add a docstring to every function in utils.py.`
4. The second session is told the file is busy and waits. When the first finishes, the second wakes up, reads what changed, and makes its edit on top.
5. Run `/file-baton:status` in either session to see who holds what.

## What the sessions see

The waiting session:

```
file-baton: utils.py is being edited by another Claude session (3f2a91c0).
Their task: "Add input validation to every function in utils.py, then run the tests."
You are #1 in line. Do other parts of your task first, and do not work around this
by changing the file another way (for example with a shell command).
When they finish you will be given the file with a summary of their changes; if you
are idle by then you will be woken up automatically.
```

When the file reaches it:

```
file-baton: you now hold utils.py (you were waiting for it).
Session 3f2a91c0 finished with it. Their task: "Add input validation to ..."
Their notes:
  - every function now raises ValueError on bad input; tests updated
Their changes:
--- a/utils.py
+++ b/utils.py
@@ -10,6 +10,9 @@
...
Re-read the file before editing; your earlier view of it is out of date.
```

## Commands

| Command | What it does |
|---|---|
| `/file-baton:status` | Files being edited, who is waiting, uncommitted changes per session, live sessions |
| `/file-baton:release <file>...` | Release files by hand (also `--mine`, `--all`); whoever waits gets them next |

Claude leaves handoff notes on its own when someone is waiting, with `file-baton note`.

## Settings

Defaults work for most people. To change them, set environment variables before starting `claude`:

| Variable | Default | Meaning |
|---|---|---|
| `FILE_BATON_AUTO_RESUME` | `true` | Wake an idle session when a file it waits for reaches it |
| `FILE_BATON_COMMIT_GUARD` | `true` | Refuse commits that include other sessions' changes |
| `FILE_BATON_GRANT_TIMEOUT_MINUTES` | `10` | How long a handed-over file stays reserved while others wait |
| `FILE_BATON_IDLE_RELEASE_MINUTES` | `20` | Release a session's files after this long without activity |
| `FILE_BATON_MAX_DIFF_LINES` | `200` | Lines of diff included in a handoff |
| `FILE_BATON_DISABLED` | `false` | Turn file-baton off |

To commit everything despite the guard, prefix the command with `FILE_BATON_ALLOW=1`
(Claude is told to do this only when you explicitly ask).

## What it does not cover

- Edits made through shell commands (`sed -i`, redirects, code generators). Only Claude's edit tools are locked.
- Conflicts between different files, such as renaming a function in one file while another session calls it elsewhere.
- `git commit` typed by a person in a terminal. The guard sees only Claude's commands.
- Sessions on different machines, and Windows.

If your sessions don't need to share a checkout, `claude --worktree <name>` gives each one
its own and avoids all of this.

## Uninstall

```bash
claude plugin uninstall file-baton@file-baton
claude plugin marketplace remove file-baton
rm -rf .git/file-baton   # per repository: the lock state and log
```

## Troubleshooting

- `/file-baton:status` shows the current state; the log of recent decisions is in `.git/file-baton/log`.
- A file stuck with a session you already closed: `/file-baton:release <file>`.
- On any internal error file-baton steps aside (the edit goes through) and logs the error.

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

## Development

```bash
make build      # dev binary in libexec/dev/ (gitignored, preferred by the launcher)
make test       # go vet + go test -race
make validate   # claude plugin validate --strict .
make e2e        # two real Claude sessions racing for one file (costs a few cents)
claude --plugin-dir .   # try the checkout without installing it
```

Releasing: bump `version` in `.claude-plugin/plugin.json`, run `make release`, commit
`libexec/` and the manifest, then `claude plugin tag --push`. Installs are git clones, so the
release binaries under `libexec/<os>-<arch>/` are committed.
