// Package engine holds file-baton's rules. It works on a loaded State and
// reaches the outside world (clock, processes, files, git) only through Env,
// so every rule can be tested without any of them.
package engine

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/KaitouKid1412/file-baton/internal/config"
	"github.com/KaitouKid1412/file-baton/internal/state"
)

// Reasons a lock changed hands.
const (
	ReasonReleased  = "released"  // the owner's turn ended
	ReasonCrashed   = "crashed"   // the owner's process is gone
	ReasonIdle      = "idle"      // the owner showed no activity for too long
	ReasonEnded     = "ended"     // the owner's session ended
	ReasonForced    = "forced"    // the user released it by hand
	ReasonCommitted = "committed" // the owner committed its changes
	ReasonExpired   = "expired"   // the owner has not edited it for the hold limit
)

// Note limits.
const (
	MaxNotes   = 5
	MaxNoteLen = 1000
	MaxTaskLen = 300
)

// Env is everything the rules need from outside the state.
type Env interface {
	Now() time.Time
	// Alive reports whether the session's Claude process still runs.
	Alive(s *state.Session) bool
	// LastActivity is the latest sign of life from the session.
	LastActivity(s *state.Session) time.Time
	// Snapshot copies the file aside and returns the snapshot name ("" if absent).
	Snapshot(path, sid string) string
	DropSnapshot(name string)
	// Diff describes how the file changed since the lock's snapshot.
	Diff(l *state.Lock) (diff string, changed bool)
	// IsClean reports whether git sees no uncommitted change in the file.
	IsClean(path string) bool
}

// Engine applies the rules to one loaded state.
type Engine struct {
	St  *state.State
	Env Env
	Cfg config.Config
	// Rel turns an absolute path into the form shown to Claude.
	Rel func(path string) string
	// CLI is the command line that runs the file-baton CLI.
	CLI string
}

// Decision is the answer to an edit attempt.
type Decision struct {
	Deny   bool
	Reason string
}

// Seen records activity from a session, creating it on first sight.
func (e *Engine) Seen(sid string, pid int, transcript string) *state.Session {
	now := e.Env.Now()
	s := e.St.Sessions[sid]
	if s == nil {
		s = &state.Session{ID: sid, StartedAt: now}
		e.St.Sessions[sid] = s
	}
	if pid > 0 {
		s.PID = pid
	}
	if transcript != "" {
		s.Transcript = transcript
	}
	s.LastSeen = now
	return s
}

// SetTask records the session's latest prompt as its current task.
// Notifications Claude Code delivers as prompts (a background hook waking the
// session, a finished background task) are not tasks and are ignored.
func (e *Engine) SetTask(sid, prompt string) {
	if strings.HasPrefix(strings.TrimSpace(prompt), "<task-notification>") {
		return
	}
	if s := e.St.Sessions[sid]; s != nil {
		s.Task = oneLine(prompt, MaxTaskLen)
	}
}

// Reap removes dead sessions and expires holds and grants nobody is using.
func (e *Engine) Reap() {
	for id, s := range e.St.Sessions {
		if !e.Env.Alive(s) {
			e.leave(id, ReasonCrashed)
		}
	}
	now := e.Env.Now()
	for _, path := range e.lockPaths() {
		l := e.St.Locks[path]
		if l == nil {
			continue
		}
		l.Queue = slices.DeleteFunc(l.Queue, func(w state.Waiter) bool {
			return e.St.Sessions[w.Session] == nil
		})
		owner := e.St.Sessions[l.Owner]
		switch {
		case owner == nil && l.Status == state.Held:
			e.release(l, ReasonCrashed)
		case owner == nil:
			e.passOn(l)
		case l.Status == state.Held && now.Sub(e.Env.LastActivity(owner)) > e.Cfg.IdleRelease:
			e.release(l, ReasonIdle)
		case l.Status == state.Held && e.Cfg.HoldTimeout > 0 && now.Sub(lastEdit(l)) > e.Cfg.HoldTimeout:
			e.release(l, ReasonExpired)
		case l.Status == state.Granted:
			e.expireGrant(l)
		}
	}
}

// Acquire decides an edit of path by session sid.
func (e *Engine) Acquire(sid, path string) Decision {
	l := e.St.Locks[path]
	if l == nil {
		now := e.Env.Now()
		e.St.Locks[path] = &state.Lock{
			Path:     path,
			Owner:    sid,
			Status:   state.Held,
			Since:    now,
			LastEdit: now,
			Snapshot: e.Env.Snapshot(path, sid),
		}
		return Decision{}
	}
	if l.Owner != sid {
		e.enqueue(l, sid)
		e.expireGrant(l)
		if l.Owner != sid {
			return Decision{Deny: true, Reason: e.blockedText(l, sid)}
		}
	}
	if l.Status == state.Granted {
		text := e.handoffText(l)
		e.hold(l)
		return Decision{Deny: true, Reason: text}
	}
	l.LastEdit = e.Env.Now()
	return Decision{}
}

// ReleaseCommitted releases the session's held files that git now sees as
// clean, which after a commit means the commit took them. It returns the files
// released and, of those, the ones handed straight to a waiting session.
func (e *Engine) ReleaseCommitted(sid string) (released, handedOn []string) {
	for _, path := range e.lockPaths() {
		l := e.St.Locks[path]
		if l == nil || l.Owner != sid || l.Status != state.Held || !e.Env.IsClean(path) {
			continue
		}
		e.release(l, ReasonCommitted)
		released = append(released, path)
		if next := e.St.Locks[path]; next != nil && next.Owner != sid {
			handedOn = append(handedOn, path)
		}
	}
	return released, handedOn
}

// EndTurn releases every lock the session held during its turn. Locks granted
// to it while it waited are kept for delivery.
func (e *Engine) EndTurn(sid string) {
	for _, path := range e.lockPaths() {
		if l := e.St.Locks[path]; l != nil && l.Owner == sid && l.Status == state.Held {
			e.release(l, ReasonReleased)
		}
	}
}

// EndSession releases everything the session holds and takes it out of every queue.
func (e *Engine) EndSession(sid string) {
	e.leave(sid, ReasonEnded)
}

// Release frees one lock by hand, whoever holds it. It reports whether a lock existed.
func (e *Engine) Release(path string) bool {
	l := e.St.Locks[path]
	if l == nil {
		return false
	}
	if l.Status == state.Held {
		e.release(l, ReasonForced)
	} else {
		e.passOn(l)
	}
	return true
}

// Waiting reports whether the session is queued for a lock or has one granted
// to it that it has not been told about yet.
func (e *Engine) Waiting(sid string) bool {
	for _, l := range e.St.Locks {
		if l.Owner == sid && l.Status == state.Granted {
			return true
		}
		if slices.ContainsFunc(l.Queue, func(w state.Waiter) bool { return w.Session == sid }) {
			return true
		}
	}
	return false
}

// TakeDeliveries hands the session every lock granted to it and returns the
// handoff text, or "" when nothing was waiting for it.
func (e *Engine) TakeDeliveries(sid string) string {
	var texts []string
	for _, path := range e.lockPaths() {
		l := e.St.Locks[path]
		if l != nil && l.Owner == sid && l.Status == state.Granted {
			texts = append(texts, e.handoffText(l))
			e.hold(l)
		}
	}
	return strings.Join(texts, "\n\n")
}

// HeadsUp returns a reminder for a holder about sessions newly waiting on its
// files, or "" when there is nothing new to say.
func (e *Engine) HeadsUp(sid string) string {
	var rels []string
	for _, path := range e.lockPaths() {
		l := e.St.Locks[path]
		if l == nil || l.Owner != sid || l.Status != state.Held {
			continue
		}
		fresh := false
		for _, w := range l.Queue {
			if !slices.Contains(l.Told, w.Session) {
				l.Told = append(l.Told, w.Session)
				fresh = true
			}
		}
		if fresh {
			rels = append(rels, e.Rel(path))
		}
	}
	if len(rels) == 0 {
		return ""
	}
	return e.headsUpText(rels)
}

// AddNote attaches a handoff note to a lock the session holds.
func (e *Engine) AddNote(sid, path, note string) error {
	l := e.St.Locks[path]
	if l == nil || l.Owner != sid {
		return fmt.Errorf("you do not hold %s; notes can only be left on files you are editing", e.Rel(path))
	}
	note = strings.TrimSpace(note)
	if note == "" {
		return fmt.Errorf("the note is empty")
	}
	if len(l.Notes) >= MaxNotes {
		return fmt.Errorf("%s already has %d notes", e.Rel(path), MaxNotes)
	}
	l.Notes = append(l.Notes, truncate(note, MaxNoteLen))
	return nil
}

// HeldBy returns the paths of the locks the session owns, sorted. With
// waitedOnly, only those other sessions are queued for.
func (e *Engine) HeldBy(sid string, waitedOnly bool) []string {
	var paths []string
	for _, path := range e.lockPaths() {
		l := e.St.Locks[path]
		if l.Owner == sid && (!waitedOnly || len(l.Queue) > 0) {
			paths = append(paths, path)
		}
	}
	return paths
}

// PruneTouched forgets uncommitted-change records for files git sees as clean.
func (e *Engine) PruneTouched() {
	for path := range e.St.Touched {
		if e.Env.IsClean(path) {
			delete(e.St.Touched, path)
		}
	}
}

// Foreign is another session's uncommitted change that a commit would include.
type Foreign struct {
	Path    string
	Session string
	Task    string
	Ended   bool
}

// ForeignChanges lists other sessions' changes in the paths covered selects.
func (e *Engine) ForeignChanges(sid string, covered func(path string) bool) []Foreign {
	seen := map[[2]string]bool{}
	var out []Foreign
	add := func(path, session, task string) {
		key := [2]string{path, session}
		if session == sid || seen[key] || !covered(path) {
			return
		}
		seen[key] = true
		s := e.St.Sessions[session]
		if s != nil && s.Task != "" {
			task = s.Task
		}
		out = append(out, Foreign{Path: path, Session: session, Task: task, Ended: s == nil})
	}
	for path, touches := range e.St.Touched {
		for _, t := range touches {
			add(path, t.Session, t.Task)
		}
	}
	for path, l := range e.St.Locks {
		if l.Status == state.Held {
			add(path, l.Owner, "")
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Session < out[j].Session
	})
	return out
}

// leave removes a session entirely, handing on whatever it held.
func (e *Engine) leave(sid, reason string) {
	for _, path := range e.lockPaths() {
		l := e.St.Locks[path]
		if l == nil {
			continue
		}
		l.Queue = slices.DeleteFunc(l.Queue, func(w state.Waiter) bool { return w.Session == sid })
		if l.Owner != sid {
			continue
		}
		if l.Status == state.Held {
			e.release(l, reason)
		} else {
			e.passOn(l)
		}
	}
	delete(e.St.Sessions, sid)
}

// release ends the owner's hold: it records the owner's uncommitted change,
// builds the handoff and grants the lock to the next live waiter.
func (e *Engine) release(l *state.Lock, reason string) {
	task := ""
	if s := e.St.Sessions[l.Owner]; s != nil {
		task = s.Task
	}
	diff, changed := e.Env.Diff(l)
	// A change the owner already committed is nobody's uncommitted work.
	if changed && !e.Env.IsClean(l.Path) {
		e.addTouch(l.Path, l.Owner, task)
	}
	e.Env.DropSnapshot(l.Snapshot)
	e.grantNext(l, &state.Handoff{
		From:     l.Owner,
		FromTask: task,
		Reason:   reason,
		Notes:    l.Notes,
		Diff:     diff,
		At:       e.Env.Now(),
	})
}

// passOn moves an undelivered grant, handoff included, to the next waiter.
func (e *Engine) passOn(l *state.Lock) {
	h := l.Handoff
	if h == nil {
		h = &state.Handoff{From: l.Owner, Reason: ReasonEnded, At: e.Env.Now()}
	}
	e.grantNext(l, h)
}

func (e *Engine) grantNext(l *state.Lock, h *state.Handoff) {
	next := e.popLiveWaiter(l)
	if next == "" {
		delete(e.St.Locks, l.Path)
		return
	}
	l.Owner = next
	l.Status = state.Granted
	l.Since = e.Env.Now()
	l.Snapshot = ""
	l.Notes = nil
	l.Told = nil
	l.Handoff = h
}

func (e *Engine) popLiveWaiter(l *state.Lock) string {
	for len(l.Queue) > 0 {
		w := l.Queue[0]
		l.Queue = l.Queue[1:]
		if s := e.St.Sessions[w.Session]; s != nil && e.Env.Alive(s) {
			return w.Session
		}
	}
	return ""
}

// expireGrant passes a grant its owner has ignored past the timeout to the
// next waiter and puts the ignoring session back at the end of the line.
func (e *Engine) expireGrant(l *state.Lock) {
	if l.Status != state.Granted || len(l.Queue) == 0 || e.Env.Now().Sub(l.Since) <= e.Cfg.GrantTimeout {
		return
	}
	old := l.Owner
	e.passOn(l)
	if cur := e.St.Locks[l.Path]; cur != nil && e.St.Sessions[old] != nil {
		e.enqueue(cur, old)
	}
}

// hold turns a granted lock into one its owner is editing.
func (e *Engine) hold(l *state.Lock) {
	l.Status = state.Held
	l.Since = e.Env.Now()
	l.LastEdit = l.Since
	l.Snapshot = e.Env.Snapshot(l.Path, l.Owner)
	l.Handoff = nil
}

func (e *Engine) enqueue(l *state.Lock, sid string) {
	if !slices.ContainsFunc(l.Queue, func(w state.Waiter) bool { return w.Session == sid }) {
		l.Queue = append(l.Queue, state.Waiter{Session: sid, Since: e.Env.Now()})
	}
}

func (e *Engine) addTouch(path, sid, task string) {
	now := e.Env.Now()
	touches := e.St.Touched[path]
	for i := range touches {
		if touches[i].Session == sid {
			touches[i].At = now
			if task != "" {
				touches[i].Task = task
			}
			return
		}
	}
	e.St.Touched[path] = append(touches, state.Touch{Session: sid, Task: task, At: now})
}

// lastEdit is when the owner last edited the file; locks saved before
// LastEdit existed fall back to when they were taken.
func lastEdit(l *state.Lock) time.Time {
	if l.LastEdit.IsZero() {
		return l.Since
	}
	return l.LastEdit
}

// lockPaths returns the lock keys sorted, so iteration that mutates the map is
// safe and deterministic.
func (e *Engine) lockPaths() []string {
	paths := make([]string, 0, len(e.St.Locks))
	for p := range e.St.Locks {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

func oneLine(s string, max int) string {
	return truncate(strings.Join(strings.Fields(s), " "), max)
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

// Short is the abbreviated session id shown to people and Claude.
func Short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
