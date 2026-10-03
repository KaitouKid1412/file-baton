package engine

import (
	"fmt"
	"slices"
	"strings"

	"github.com/KaitouKid1412/file-baton/internal/state"
)

func (e *Engine) cli() string { return `"` + e.CLI + `"` }

func (e *Engine) blockedText(l *state.Lock, sid string) string {
	var b strings.Builder
	who := Short(l.Owner)
	if l.Status == state.Granted {
		fmt.Fprintf(&b, "file-baton: %s has been handed to another Claude session (%s) that was waiting for it.\n", e.Rel(l.Path), who)
	} else {
		fmt.Fprintf(&b, "file-baton: %s is being edited by another Claude session (%s).\n", e.Rel(l.Path), who)
	}
	if s := e.St.Sessions[l.Owner]; s != nil && s.Task != "" {
		fmt.Fprintf(&b, "Their task: %q\n", s.Task)
	}
	pos := slices.IndexFunc(l.Queue, func(w state.Waiter) bool { return w.Session == sid }) + 1
	fmt.Fprintf(&b, "You are #%d in line. Do other parts of your task first, and do not work around this by changing the file another way (for example with a shell command).\n", pos)
	if e.Cfg.AutoResume {
		b.WriteString("When they finish you will be given the file with a summary of their changes; if you are idle by then you will be woken up automatically.")
	} else {
		b.WriteString("When they finish you will be given the file with a summary of their changes on your next attempt. Retry this edit later.")
	}
	return b.String()
}

func (e *Engine) handoffText(l *state.Lock) string {
	var b strings.Builder
	rel := e.Rel(l.Path)
	fmt.Fprintf(&b, "file-baton: you now hold %s (you were waiting for it).\n", rel)
	h := l.Handoff
	if h == nil {
		h = &state.Handoff{}
	}
	from := Short(h.From)
	task := ""
	if h.FromTask != "" {
		task = fmt.Sprintf(" Their task: %q", h.FromTask)
	}
	switch h.Reason {
	case ReasonCrashed:
		fmt.Fprintf(&b, "Session %s stopped running before finishing; its unfinished changes are below.%s\n", from, task)
	case ReasonIdle:
		fmt.Fprintf(&b, "Session %s went quiet and its hold expired; its changes so far are below.%s\n", from, task)
	case ReasonEnded:
		fmt.Fprintf(&b, "Session %s exited.%s\n", from, task)
	case ReasonForced:
		fmt.Fprintf(&b, "The user released it from session %s.%s\n", from, task)
	default:
		fmt.Fprintf(&b, "Session %s finished with it.%s\n", from, task)
	}
	if len(h.Notes) > 0 {
		b.WriteString("Their notes:\n")
		for _, n := range h.Notes {
			fmt.Fprintf(&b, "  - %s\n", n)
		}
	}
	if h.Diff == "" {
		b.WriteString("Their changes: none.\n")
	} else {
		b.WriteString("Their changes:\n")
		b.WriteString(strings.TrimRight(h.Diff, "\n"))
		b.WriteString("\n")
	}
	b.WriteString("Re-read the file before editing; your earlier view of it is out of date.\n")
	b.WriteString(releasedAutomatically)
	return b.String()
}

// releasedAutomatically keeps Claude from looking for a way to release files.
const releasedAutomatically = "Files are released automatically when your turn ends; there is no command to release or finish them."

func (e *Engine) headsUpText(rels []string) string {
	return fmt.Sprintf("file-baton: another Claude session is now waiting for %s, which you are editing. "+
		"Before you finish, leave a short note on what you changed and why, so they can pick up cleanly:\n  %s note %s \"<what changed and why>\"\n%s",
		strings.Join(rels, ", "), e.cli(), quoteArg(rels[0]), releasedAutomatically)
}

func (e *Engine) notesRequestText(rels []string) string {
	return fmt.Sprintf("file-baton: other Claude sessions are waiting for files you edited: %s. "+
		"They will receive your diff automatically. If anything about your changes is not obvious from the diff "+
		"(intent, follow-ups, things to avoid), leave a note now:\n  %s note <file> \"<note>\"\n"+
		"Then simply finish your turn, or finish right away if no note is needed. %s",
		strings.Join(rels, ", "), e.cli(), releasedAutomatically)
}

// ForeignText explains a refused commit.
func (e *Engine) ForeignText(list []Foreign) string {
	var b strings.Builder
	b.WriteString("file-baton: this commit would include changes made by other Claude sessions:\n")
	var rels []string
	for _, f := range list {
		rel := e.Rel(f.Path)
		if !slices.Contains(rels, rel) {
			rels = append(rels, rel)
		}
		ended := ""
		if f.Ended {
			ended = " (ended)"
		}
		task := ""
		if f.Task != "" {
			task = fmt.Sprintf(" %q", f.Task)
		}
		fmt.Fprintf(&b, "  %s  session %s%s%s\n", rel, Short(f.Session), ended, task)
	}
	b.WriteString("Commit only your own changes instead, by naming your files:\n")
	b.WriteString("  git commit -m \"...\" -- <your files>\n")
	quoted := make([]string, len(rels))
	for i, r := range rels {
		quoted[i] = quoteArg(r)
	}
	fmt.Fprintf(&b, "If their changes are staged, unstage them first: git restore --staged %s\n", strings.Join(quoted, " "))
	b.WriteString("Only if the user explicitly asked to commit everything, run the command again prefixed with FILE_BATON_ALLOW=1.")
	return b.String()
}

// quoteArg quotes a path for a shell when it needs it.
func quoteArg(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"\\$`!*?[]{}()<>|&;#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
