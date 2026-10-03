// Package state holds file-baton's shared state and the store that persists it.
//
// The state is one JSON document per repository, kept under the git common
// directory so every worktree of the repository shares it. Every change runs
// under an exclusive flock, which the kernel releases if the process dies.
package state

import "time"

// SchemaVersion is the version of the JSON document this build reads and writes.
const SchemaVersion = 1

// Lock statuses.
const (
	Held    = "held"    // the owner is (or may be) editing the file this turn
	Granted = "granted" // reserved for the owner; its handoff may still be undelivered
)

// State is the whole shared document.
type State struct {
	Version  int                 `json:"version"`
	Sessions map[string]*Session `json:"sessions"`
	Locks    map[string]*Lock    `json:"locks"`
	Touched  map[string][]Touch  `json:"touched"`
}

// Session is one Claude Code session that has used the repository.
type Session struct {
	ID         string    `json:"id"`
	PID        int       `json:"pid"`
	Transcript string    `json:"transcript,omitempty"`
	Task       string    `json:"task,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	LastSeen   time.Time `json:"last_seen"`
	WaiterPID  int       `json:"waiter_pid,omitempty"`
}

// Lock is the baton for one file.
type Lock struct {
	Path       string   `json:"path"`
	Owner      string   `json:"owner"`
	Status     string   `json:"status"`
	Since      time.Time `json:"since"`
	Snapshot   string   `json:"snapshot,omitempty"`
	Queue      []Waiter `json:"queue,omitempty"`
	Notes      []string `json:"notes,omitempty"`
	Handoff    *Handoff `json:"handoff,omitempty"`
	Told       []string `json:"told,omitempty"`
	NotesAsked bool     `json:"notes_asked,omitempty"`
}

// Waiter is a session queued for a lock.
type Waiter struct {
	Session string    `json:"session"`
	Since   time.Time `json:"since"`
}

// Handoff is what the next owner of a lock is told about the previous one.
type Handoff struct {
	From      string    `json:"from"`
	FromTask  string    `json:"from_task,omitempty"`
	Reason    string    `json:"reason"`
	Notes     []string  `json:"notes,omitempty"`
	Diff      string    `json:"diff,omitempty"`
	At        time.Time `json:"at"`
	Delivered bool      `json:"delivered,omitempty"`
}

// Touch records that a session left uncommitted changes in a file.
type Touch struct {
	Session string    `json:"session"`
	Task    string    `json:"task,omitempty"`
	At      time.Time `json:"at"`
}

// New returns an empty state.
func New() *State {
	return &State{
		Version:  SchemaVersion,
		Sessions: map[string]*Session{},
		Locks:    map[string]*Lock{},
		Touched:  map[string][]Touch{},
	}
}

// normalize fills nil maps so callers never check for them.
func (s *State) normalize() {
	if s.Sessions == nil {
		s.Sessions = map[string]*Session{}
	}
	if s.Locks == nil {
		s.Locks = map[string]*Lock{}
	}
	if s.Touched == nil {
		s.Touched = map[string][]Touch{}
	}
}
