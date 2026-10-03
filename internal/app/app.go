// Package app connects the engine to the real world: the repository, the
// store, the processes and the log. Hooks and CLI commands both start here.
package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/KaitouKid1412/file-baton/internal/config"
	"github.com/KaitouKid1412/file-baton/internal/engine"
	"github.com/KaitouKid1412/file-baton/internal/gitx"
	"github.com/KaitouKid1412/file-baton/internal/proc"
	"github.com/KaitouKid1412/file-baton/internal/state"
)

// App is file-baton bound to one work tree.
type App struct {
	Repo   gitx.Repo
	Store  *state.Store
	Cfg    config.Config
	CLI    string
	Getenv func(string) string
	log    *Logger
}

// Open binds file-baton to the work tree containing dir. It returns
// gitx.ErrNoRepo outside a git work tree.
func Open(dir string, getenv func(string) string) (*App, error) {
	repo, err := gitx.Discover(dir)
	if err != nil {
		return nil, err
	}
	store, err := state.Open(filepath.Join(repo.CommonDir, "file-baton"))
	if err != nil {
		return nil, err
	}
	cfg, warnings := config.Load(getenv)
	a := &App{
		Repo:   repo,
		Store:  store,
		Cfg:    cfg,
		CLI:    cliPath(getenv),
		Getenv: getenv,
		log:    &Logger{Path: filepath.Join(store.Dir, "log")},
	}
	for _, w := range warnings {
		a.Logf("-", "config", "%s", w)
	}
	return a, nil
}

// Update loads the state, reaps dead sessions and runs fn with an engine over
// it, saving the result unless fn fails.
func (a *App) Update(fn func(e *engine.Engine) error) error {
	return a.Store.Update(func(st *state.State) error {
		e := a.Engine(st)
		e.Reap()
		return fn(e)
	})
}

// Engine returns an engine over st bound to this work tree.
func (a *App) Engine(st *state.State) *engine.Engine {
	return &engine.Engine{
		St:  st,
		Env: &env{app: a, now: time.Now()},
		Cfg: a.Cfg,
		Rel: a.Repo.Rel,
		CLI: a.CLI,
	}
}

// Logf appends one line to the log.
func (a *App) Logf(sid, event, format string, args ...any) {
	a.log.Printf("%s %s %s", engine.Short(sid), event, fmt.Sprintf(format, args...))
}

// LogPath is where the log is written.
func (a *App) LogPath() string { return a.log.Path }

// PID is the Claude process id from the environment, 0 when absent.
func PID(getenv func(string) string) int {
	pid, _ := strconv.Atoi(getenv("CLAUDE_PID"))
	return pid
}

func cliPath(getenv func(string) string) string {
	if root := getenv("CLAUDE_PLUGIN_ROOT"); root != "" {
		return filepath.Join(root, "libexec", "file-baton")
	}
	if exe, err := os.Executable(); err == nil {
		return exe
	}
	return "file-baton"
}

// env is the engine's view of the outside world for one update.
type env struct {
	app *App
	now time.Time
}

func (e *env) Now() time.Time { return e.now }

func (e *env) Alive(s *state.Session) bool {
	if s.PID > 0 {
		return proc.Alive(s.PID)
	}
	return e.now.Sub(e.LastActivity(s)) < e.app.Cfg.IdleRelease
}

func (e *env) LastActivity(s *state.Session) time.Time {
	last := s.LastSeen
	if s.Transcript != "" {
		if st, err := os.Stat(s.Transcript); err == nil && st.ModTime().After(last) {
			last = st.ModTime()
		}
	}
	return last
}

func (e *env) Snapshot(path, sid string) string {
	name, err := e.app.Store.WriteSnapshot(path, sid)
	if err != nil {
		e.app.Logf(sid, "snapshot", "%s: %v", path, err)
	}
	return name
}

func (e *env) DropSnapshot(name string) { e.app.Store.RemoveSnapshot(name) }

func (e *env) Diff(l *state.Lock) (string, bool) {
	snapshot := ""
	if l.Snapshot != "" {
		snapshot = e.app.Store.SnapshotPath(l.Snapshot)
	}
	return gitx.SnapshotDiff(snapshot, l.Path, e.app.Repo.Rel(l.Path), e.app.Cfg.MaxDiffLines)
}

func (e *env) IsClean(path string) bool { return gitx.IsClean(path) }
