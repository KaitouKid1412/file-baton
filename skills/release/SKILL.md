---
name: release
description: Release file-baton locks by hand, for example a file held by a stuck session.
argument-hint: <file>... | --all | --mine
disable-model-invocation: true
allowed-tools: Bash("${CLAUDE_PLUGIN_ROOT}/libexec/file-baton" release *)
---
!`"${CLAUDE_PLUGIN_ROOT}/libexec/file-baton" release $ARGUMENTS`

Tell the user the result above in one sentence. If it printed a usage message, explain the forms: `/file-baton:release <file>...` releases specific files, `--mine` releases this session's files, and `--all` releases every file. Anyone waiting for a released file receives it next.
