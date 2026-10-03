# Plan 05: packaging, testing and release

## Plugin layout

```
.claude-plugin/plugin.json        name, version, description, author
.claude-plugin/marketplace.json   one entry, source "./"
hooks/hooks.json                  exec-form hooks
skills/status/SKILL.md
skills/release/SKILL.md
libexec/file-baton                POSIX sh launcher
libexec/<os>-<arch>/file-baton    release binaries, committed with each release
libexec/dev/file-baton            development build (gitignored), preferred when present
```

## Launcher (`libexec/file-baton`)

- Run `libexec/dev/file-baton` if present, else map `uname -s`/`uname -m` to `darwin|linux` × `amd64|arm64` and `exec` the matching binary with `"$@"`.
- Missing binary: hooks exit 0 (fail open); `hook session-start` prints a `systemMessage` saying file-baton is inactive and to update or reinstall the plugin. (The first version said "run make build", which is useless to someone who installed the plugin.)

## hooks.json

Exec form (`"command": "${CLAUDE_PLUGIN_ROOT}/libexec/file-baton", "args": ["hook", "<event>"]`)
for every event in the overview's hook map. Timeouts: `wait` 3600 s with `asyncRewake: true`;
`session-end` 5; everything else 10.

## Build

`Makefile`:
- `build`: host binary into `libexec/dev/` (used by `--plugin-dir` development)
- `dist`: all four targets, `CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)"`
- `test`: `go vet ./... && go test -race ./...`
- `validate`: `claude plugin validate --strict .`
- `release`: `test`, `validate`, `dist`, then prints the commit and tag commands
- `e2e`: end-to-end script below

Installs are git clones, so release binaries are committed; `.gitignore` covers only
`libexec/dev/` and `dist/`.

## Tests

1. Unit: `engine` rules with a fake clock and liveness; `gitx` commit parser tables; config.
2. Integration (`go test`, tag-free): build the binary once into a temp dir, create a temp git repo, drive hooks through stdin with two or three session ids. Alive sessions use the test process pid; a dead session uses the pid of an exited child.
3. End-to-end (`scripts/e2e.sh`, manual, costs a few cents): two `claude -p --plugin-dir . --model haiku` sessions in a temp repo, both asked to edit the same file; assert from the log and the final file that the edits were serialized and the second session received a handoff.

## Release

1. Bump `version` in `plugin.json`.
2. `make release`.
3. Commit the binaries with the version bump; tag with `claude plugin tag --push`.
4. Users: `claude plugin marketplace add <owner>/file-baton` then `claude plugin install file-baton@file-baton`.

Binary size: about 3 MB per target, about 12 MB per release in git history. If that
grows into a problem, move binaries to GitHub Release assets downloaded by the launcher.

## Tasks

- [ ] Launcher, hooks.json, plugin.json, marketplace.json
- [ ] Makefile, .gitignore
- [ ] README: what it does, install, how it behaves, limits, config, troubleshooting
- [ ] Integration tests; e2e script; one real e2e run
- [ ] `claude plugin validate --strict .` clean
