# Plan 03: commit guard

Goal: a session's `git commit` never sweeps in another session's uncommitted work, the
failure the issue #88862 team measured.

## Ownership of uncommitted changes

Batons are released at the end of a turn, long before anyone commits, so ownership is
tracked separately:

```go
type Touch struct {
    Session string    `json:"session"`
    Task    string    `json:"task"`
    At      time.Time `json:"at"`
}
```

- Added in `release` when the snapshot diff is non-empty (one entry per session per path; refresh `At`).
- A session "owns changes" in a path if it holds the path's lock now or has a `Touch` on it.
- Pruned when the path is clean: `git status --porcelain -- <path>` prints nothing (committed, reverted or deleted-and-committed). Pruning runs in the guard and in `status`.
- Touches survive their session ending: the changes are still in the working tree.

## Detecting a commit (pre-bash)

Split `tool_input.command` into segments on `&&`, `||`, `;`, `|`, newlines (respecting
quotes). A segment is a commit if, after optional `VAR=value` prefixes, it runs `git`, skips
global options (`-C <dir>`, `-c <k=v>`, `--no-pager`, `--git-dir=…`, `--work-tree=…`), and
the subcommand is `commit`.

## What the commit would include

Evaluated in the repo of `-C <dir>` if given, else `cwd`:

| Command shape | Commit set |
|---|---|
| `git commit` | staged: `git diff --cached --name-only -z` |
| `-a` / `--all` / combined short flags with `a` (`-am`) | staged + unstaged tracked (`git diff --name-only -z`) |
| pathspecs (`git commit -m x -- a b`, `git commit a b`) | exactly those paths |
| earlier segment `git add -A` / `--all` / `.` | + unstaged tracked + untracked (`git ls-files -o --exclude-standard -z`) |
| earlier segment `git add -u` | + unstaged tracked |
| earlier segment `git add <paths>` | + those paths |

Options that take a value (`-m`, `-F`, `-C`, `-c`, `--author`, `--date`, `--fixup`,
`--squash`, `-t`, `--template`, `--cleanup`, `--trailer`) are skipped when looking for
pathspecs. This is best effort; anything unparseable falls back to "staged".

## Decision

`foreign` = paths in the commit set owned by any session other than the caller. Empty →
no output. Otherwise deny unless the segment carries `FILE_BATON_ALLOW=1`:

```
file-baton: this commit would include changes made by other Claude sessions:
  src/api.ts    session 3f2a91c0 "add pagination to the list endpoint"
  README.md     session 77bd02e1 (ended) "document the CLI"
Commit only your own files instead, for example:
  git restore --staged src/api.ts README.md   then commit
  or: git commit -m "..." -- <your files>
Only if the user explicitly asked to commit everything, prefix the command with
FILE_BATON_ALLOW=1.
```

`commit_guard` config (default true) turns the guard off.

## Tasks

- [ ] `Touch` recording in `engine.release`; pruning helper with `gitx.IsClean`
- [ ] `gitx/commitparse.go`: segment splitter with quote handling, commit detection, commit-set rules
- [ ] `gitx.Staged`, `Unstaged`, `Untracked`
- [ ] `hook pre-bash`: guard + message
- [ ] Tests: parser table (quotes, `-C`, `-am`, pathspecs, chained `git add -A && git commit`), foreign detection, override, pruning after commit

## Done when

- B's `git commit -am` with A's uncommitted change in the tree is denied, naming A's task.
- After A commits its file, B's commit goes through.
