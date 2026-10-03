// Command file-baton coordinates Claude Code sessions that share a git work
// tree: one session edits a file at a time, and the next one receives it with
// a summary of what changed.
package main

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/KaitouKid1412/file-baton/internal/cli"
	"github.com/KaitouKid1412/file-baton/internal/hook"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `file-baton coordinates Claude Code sessions editing the same git work tree.

Usage:
  file-baton note <file> <text...>     leave a handoff note on a file you hold
  file-baton note --all <text...>      leave the note on every file you hold
  file-baton status [--json|--log]     show locks, queues and uncommitted changes
  file-baton release <file>...         release locks by hand (also --all, --mine)
  file-baton version
  file-baton hook <event>              hook entry point (%s)
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) == 0 {
		fmt.Fprintf(stderr, usage, strings.Join(hook.Events(), ", "))
		return 1
	}
	cwd, _ := os.Getwd()
	env := cli.Env{Args: args[1:], Stdout: stdout, Stderr: stderr, Getenv: getenv, Cwd: cwd}
	switch args[0] {
	case "hook":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "file-baton: hook needs an event name")
			return 0
		}
		return hook.Run(args[1], stdin, stdout, stderr, getenv)
	case "note":
		return cli.Note(env)
	case "status":
		return cli.Status(env)
	case "release":
		return cli.Release(env)
	case "version", "--version":
		fmt.Fprintf(stdout, "file-baton %s (%s/%s, %s)\n", version, runtime.GOOS, runtime.GOARCH, runtime.Version())
		return 0
	case "help", "-h", "--help":
		fmt.Fprintf(stdout, usage, strings.Join(hook.Events(), ", "))
		return 0
	default:
		fmt.Fprintf(stderr, "file-baton: unknown command %q\n\n", args[0])
		fmt.Fprintf(stderr, usage, strings.Join(hook.Events(), ", "))
		return 1
	}
}
