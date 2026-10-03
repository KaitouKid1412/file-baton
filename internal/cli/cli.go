// Package cli implements file-baton's commands for people and for Claude:
// note, status, release and version.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/KaitouKid1412/file-baton/internal/app"
	"github.com/KaitouKid1412/file-baton/internal/engine"
	"github.com/KaitouKid1412/file-baton/internal/gitx"
	"github.com/KaitouKid1412/file-baton/internal/state"
)

// Env is what a command runs with.
type Env struct {
	Args   []string
	Stdout io.Writer
	Stderr io.Writer
	Getenv func(string) string
	Cwd    string
}

func (env Env) open() (*app.App, error) {
	a, err := app.Open(env.Cwd, env.Getenv)
	if errors.Is(err, gitx.ErrNoRepo) {
		return nil, fmt.Errorf("not inside a git repository (file-baton only works in git work trees)")
	}
	return a, err
}

func (env Env) fail(format string, args ...any) int {
	fmt.Fprintf(env.Stderr, "file-baton: "+format+"\n", args...)
	return 1
}

func (env Env) session(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	return env.Getenv("CLAUDE_CODE_SESSION_ID")
}

func (env Env) resolve(p string) string {
	if !filepath.IsAbs(p) {
		p = filepath.Join(env.Cwd, p)
	}
	return gitx.Resolve(p)
}

// splitFlags separates --flag and --flag=value arguments from positional ones.
// Everything after "--" is positional.
func splitFlags(args []string) (flags map[string]string, rest []string) {
	flags = map[string]string{}
	for i, a := range args {
		if a == "--" {
			return flags, append(rest, args[i+1:]...)
		}
		if strings.HasPrefix(a, "--") && len(a) > 2 {
			name, value, _ := strings.Cut(a[2:], "=")
			flags[name] = value
			continue
		}
		rest = append(rest, a)
	}
	return flags, rest
}

// Note attaches a handoff note to a file the calling session holds.
//
//	file-baton note <file> <text...>
//	file-baton note --all <text...>
func Note(env Env) int {
	flags, rest := splitFlags(env.Args)
	_, all := flags["all"]
	sid := env.session(flags["session"])
	if sid == "" {
		return env.fail("no session: run this from a Claude Code session or pass --session=<id>")
	}
	if (all && len(rest) < 1) || (!all && len(rest) < 2) {
		return env.fail("usage: file-baton note <file> <text...>  |  file-baton note --all <text...>")
	}
	a, err := env.open()
	if err != nil {
		return env.fail("%v", err)
	}
	var targets []string
	var text string
	if all {
		text = strings.Join(rest, " ")
	} else {
		targets = []string{env.resolve(rest[0])}
		text = strings.Join(rest[1:], " ")
	}
	var noted []string
	err = a.Update(func(e *engine.Engine) error {
		if all {
			if targets = e.HeldBy(sid, true); len(targets) == 0 {
				targets = e.HeldBy(sid, false)
			}
			if len(targets) == 0 {
				return fmt.Errorf("you are not holding any files")
			}
		}
		for _, t := range targets {
			if err := e.AddNote(sid, t, text); err != nil {
				return err
			}
			noted = append(noted, a.Repo.Rel(t))
		}
		return nil
	})
	if err != nil {
		return env.fail("%v", err)
	}
	a.Logf(sid, "note", "on %s", strings.Join(noted, ", "))
	fmt.Fprintf(env.Stdout, "file-baton: note added to %s; it will be passed to the next session.\n", strings.Join(noted, ", "))
	return 0
}

// Release frees locks by hand.
//
//	file-baton release <file>...
//	file-baton release --all
//	file-baton release --mine
func Release(env Env) int {
	flags, rest := splitFlags(env.Args)
	_, all := flags["all"]
	_, mine := flags["mine"]
	sid := env.session(flags["session"])
	if !all && !mine && len(rest) == 0 {
		return env.fail("usage: file-baton release <file>... | --all | --mine")
	}
	if mine && sid == "" {
		return env.fail("--mine needs a session: run it from a Claude Code session or pass --session=<id>")
	}
	a, err := env.open()
	if err != nil {
		return env.fail("%v", err)
	}
	var released, missing []string
	err = a.Update(func(e *engine.Engine) error {
		var targets []string
		switch {
		case all:
			for p := range e.St.Locks {
				targets = append(targets, p)
			}
		case mine:
			targets = e.HeldBy(sid, false)
		default:
			for _, p := range rest {
				targets = append(targets, env.resolve(p))
			}
		}
		sort.Strings(targets)
		for _, t := range targets {
			if e.Release(t) {
				released = append(released, a.Repo.Rel(t))
			} else {
				missing = append(missing, a.Repo.Rel(t))
			}
		}
		return nil
	})
	if err != nil {
		return env.fail("%v", err)
	}
	if len(released) == 0 && len(missing) == 0 {
		fmt.Fprintln(env.Stdout, "file-baton: nothing is locked.")
		return 0
	}
	if len(released) > 0 {
		a.Logf(sid, "release", "forced: %s", strings.Join(released, ", "))
		fmt.Fprintf(env.Stdout, "file-baton: released %s. Anyone waiting gets the file next.\n", strings.Join(released, ", "))
	}
	if len(missing) > 0 {
		fmt.Fprintf(env.Stdout, "file-baton: not locked: %s\n", strings.Join(missing, ", "))
	}
	return 0
}

// Status prints locks, queues, uncommitted changes and sessions.
//
//	file-baton status [--json | --log]
func Status(env Env) int {
	flags, _ := splitFlags(env.Args)
	a, err := env.open()
	if err != nil {
		return env.fail("%v", err)
	}
	if _, ok := flags["log"]; ok {
		return printLog(env, a.LogPath(), 40)
	}
	var snapshot state.State
	err = a.Update(func(e *engine.Engine) error {
		e.PruneTouched()
		data, err := json.Marshal(e.St)
		if err != nil {
			return err
		}
		return json.Unmarshal(data, &snapshot)
	})
	if err != nil {
		return env.fail("%v", err)
	}
	if _, ok := flags["json"]; ok {
		enc := json.NewEncoder(env.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(snapshot)
		return 0
	}
	writeStatus(env.Stdout, a, &snapshot, time.Now())
	return 0
}

func writeStatus(w io.Writer, a *app.App, st *state.State, now time.Time) {
	task := func(sid string) string {
		if s := st.Sessions[sid]; s != nil && s.Task != "" {
			return fmt.Sprintf(" %q", s.Task)
		}
		return ""
	}
	fmt.Fprintf(w, "file-baton: %s\n", a.Repo.Root)

	fmt.Fprintln(w, "\nFiles being edited:")
	paths := sortedKeys(st.Locks)
	if len(paths) == 0 {
		fmt.Fprintln(w, "  none")
	}
	for _, p := range paths {
		l := st.Locks[p]
		verb := "held by"
		if l.Status == state.Granted {
			verb = "handed to (not yet picked up)"
		}
		fmt.Fprintf(w, "  %s  %s %s for %s%s\n", a.Repo.Rel(p), verb, engine.Short(l.Owner), age(now, l.Since), task(l.Owner))
		if len(l.Queue) > 0 {
			var ws []string
			for _, q := range l.Queue {
				ws = append(ws, fmt.Sprintf("%s (%s)", engine.Short(q.Session), age(now, q.Since)))
			}
			fmt.Fprintf(w, "      waiting: %s\n", strings.Join(ws, ", "))
		}
		if len(l.Notes) > 0 {
			fmt.Fprintf(w, "      notes: %d\n", len(l.Notes))
		}
	}

	fmt.Fprintln(w, "\nUncommitted changes by session:")
	touched := sortedKeys(st.Touched)
	if len(touched) == 0 {
		fmt.Fprintln(w, "  none")
	}
	for _, p := range touched {
		for _, t := range st.Touched[p] {
			ended := ""
			if st.Sessions[t.Session] == nil {
				ended = " (ended)"
			}
			label := t.Task
			if s := st.Sessions[t.Session]; s != nil && s.Task != "" {
				label = s.Task
			}
			if label != "" {
				label = fmt.Sprintf(" %q", label)
			}
			fmt.Fprintf(w, "  %s  %s%s%s, %s ago\n", a.Repo.Rel(p), engine.Short(t.Session), ended, label, age(now, t.At))
		}
	}

	fmt.Fprintln(w, "\nSessions:")
	ids := sortedKeys(st.Sessions)
	if len(ids) == 0 {
		fmt.Fprintln(w, "  none")
	}
	for _, id := range ids {
		s := st.Sessions[id]
		fmt.Fprintf(w, "  %s  pid %d, last seen %s ago%s\n", engine.Short(id), s.PID, age(now, s.LastSeen), task(id))
	}
}

func printLog(env Env, path string, n int) int {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(env.Stdout, "file-baton: no log yet.")
		return 0
	}
	if err != nil {
		return env.fail("%v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	fmt.Fprintln(env.Stdout, strings.Join(lines, "\n"))
	return 0
}

func age(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
