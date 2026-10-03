---
name: status
description: Show which files Claude sessions in this repository are editing or waiting for, and which sessions have uncommitted changes. Use when the user asks about file-baton, file locks, or other sessions' edits.
allowed-tools: Bash("${CLAUDE_PLUGIN_ROOT}/libexec/file-baton" status)
---
!`"${CLAUDE_PLUGIN_ROOT}/libexec/file-baton" status`

Show the status above to the user as is. Then, only if something needs attention, add one short line about it: a file held for a long time, a file handed to a session that has not picked it up, or uncommitted changes left by a session that has ended.
