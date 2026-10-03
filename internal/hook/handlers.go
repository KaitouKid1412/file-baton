package hook

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"file-baton/internal/app"
	"file-baton/internal/engine"
	"file-baton/internal/gitx"
	"file-baton/internal/proc"
)

func sessionStart(c *ctx) (any, int, error) {
	return nil, 0, c.app.Update(func(e *engine.Engine) error {
		e.Seen(c.in.SessionID, c.pid, c.in.TranscriptPath)
		return nil
	})
}

func prompt(c *ctx) (any, int, error) {
	return nil, 0, c.app.Update(func(e *engine.Engine) error {
		e.Seen(c.in.SessionID, c.pid, c.in.TranscriptPath)
		e.SetTask(c.in.SessionID, c.in.Prompt)
		return nil
	})
}

func preEdit(c *ctx) (any, int, error) {
	var input struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
	}
	_ = json.Unmarshal(c.in.ToolInput, &input)
	raw := input.FilePath
	if raw == "" {
		raw = input.NotebookPath
	}
	if raw == "" {
		return nil, 0, nil
	}
	path := c.resolvePath(raw)
	if !c.app.Repo.Contains(path) || isInGitDir(c.app.Repo, path) {
		return nil, 0, nil
	}
	sid := c.in.SessionID
	var dec engine.Decision
	var heads string
	err := c.app.Update(func(e *engine.Engine) error {
		e.Seen(sid, c.pid, c.in.TranscriptPath)
		dec = e.Acquire(sid, path)
		heads = e.HeadsUp(sid)
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	rel := c.app.Repo.Rel(path)
	if dec.Deny {
		c.app.Logf(sid, "pre-edit", "deny %s", rel)
		return deny(dec.Reason), 0, nil
	}
	c.app.Logf(sid, "pre-edit", "allow %s", rel)
	return addContext(heads), 0, nil
}

func preBash(c *ctx) (any, int, error) {
	var input struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(c.in.ToolInput, &input)
	sid := c.in.SessionID

	if c.isOwnCLI(input.Command) {
		return allow("file-baton's own command", ""), 0, nil
	}

	var foreign []engine.Foreign
	var foreignApp *app.App
	if c.app.Cfg.CommitGuard {
		for _, plan := range gitx.ParseCommits(input.Command) {
			if plan.Allowed {
				c.app.Logf(sid, "pre-bash", "commit allowed by FILE_BATON_ALLOW=1")
				continue
			}
			list, a, err := c.checkCommit(plan)
			if err != nil {
				c.app.Logf(sid, "pre-bash", "commit check skipped: %v", err)
				continue
			}
			if len(list) > 0 {
				foreign, foreignApp = list, a
				break
			}
		}
	}

	var heads string
	err := c.app.Update(func(e *engine.Engine) error {
		e.Seen(sid, c.pid, c.in.TranscriptPath)
		heads = e.HeadsUp(sid)
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	if len(foreign) > 0 {
		var text string
		_ = foreignApp.Update(func(e *engine.Engine) error {
			text = e.ForeignText(foreign)
			return nil
		})
		c.app.Logf(sid, "pre-bash", "deny commit: %d foreign change(s)", len(foreign))
		return deny(text), 0, nil
	}
	return addContext(heads), 0, nil
}

// checkCommit works out which other sessions' changes a commit would include.
func (c *ctx) checkCommit(plan gitx.CommitPlan) ([]engine.Foreign, *app.App, error) {
	dir := c.in.Cwd
	if plan.Dir != "" {
		if filepath.IsAbs(plan.Dir) {
			dir = plan.Dir
		} else {
			dir = filepath.Join(c.in.Cwd, plan.Dir)
		}
	}
	a := c.app
	if repo, err := gitx.Discover(dir); err != nil {
		return nil, nil, err
	} else if repo.Root != c.app.Repo.Root {
		if a, err = app.Open(dir, c.getenv); err != nil {
			return nil, nil, err
		}
	}
	covered, err := commitCoverage(a.Repo, plan, dir)
	if err != nil {
		return nil, nil, err
	}
	var list []engine.Foreign
	err = a.Update(func(e *engine.Engine) error {
		e.PruneTouched()
		list = e.ForeignChanges(c.in.SessionID, covered)
		return nil
	})
	return list, a, err
}

// commitCoverage returns a test for whether a commit following plan would
// include a given absolute path.
func commitCoverage(repo gitx.Repo, plan gitx.CommitPlan, dir string) (func(string) bool, error) {
	files := map[string]bool{}
	var dirs []string
	addPath := func(p string) {
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		p = gitx.Resolve(p)
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			dirs = append(dirs, p)
		} else {
			files[p] = true
		}
	}
	addList := func(list []string, err error) error {
		for _, p := range list {
			files[p] = true
		}
		return err
	}
	if len(plan.Paths) > 0 {
		for _, p := range plan.Paths {
			addPath(p)
		}
	} else {
		if err := addList(repo.Staged()); err != nil {
			return nil, err
		}
		if plan.All || plan.AddAll || plan.AddUpdate {
			if err := addList(repo.Unstaged()); err != nil {
				return nil, err
			}
		}
		if plan.AddAll {
			if err := addList(repo.Untracked()); err != nil {
				return nil, err
			}
		}
		for _, p := range plan.AddPaths {
			addPath(p)
		}
	}
	return func(path string) bool {
		if !repo.Contains(path) {
			return false
		}
		if files[path] {
			return true
		}
		for _, d := range dirs {
			if path == d || strings.HasPrefix(path, d+string(filepath.Separator)) {
				return true
			}
		}
		return false
	}, nil
}

// isOwnCLI reports whether a Bash command is exactly one harmless call of this
// plugin's CLI, which is then approved without a permission prompt.
func (c *ctx) isOwnCLI(command string) bool {
	if strings.ContainsAny(command, ";&|`<>\n") || strings.Contains(command, "$(") {
		return false
	}
	segs := gitx.Segments(command)
	if len(segs) != 1 || len(segs[0]) < 2 {
		return false
	}
	words := segs[0]
	if filepath.Clean(words[0]) != filepath.Clean(c.app.CLI) {
		return false
	}
	return words[1] == "note" || words[1] == "status"
}

func stop(c *ctx) (any, int, error) {
	if c.in.AgentID != "" {
		return nil, 0, nil
	}
	sid := c.in.SessionID
	var request string
	err := c.app.Update(func(e *engine.Engine) error {
		e.Seen(sid, c.pid, c.in.TranscriptPath)
		if request = e.NotesRequest(sid); request == "" {
			e.EndTurn(sid)
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	if request != "" {
		c.app.Logf(sid, "stop", "asked for handoff notes")
		return stopBlock{Decision: "block", Reason: request}, 0, nil
	}
	c.app.Logf(sid, "stop", "released turn locks")
	return nil, 0, nil
}

// wait runs in the background after every turn (asyncRewake). While the
// session waits for a file it polls the state; when a file reaches it, it
// marks the handoff delivered and exits 2, which wakes the session with the
// handoff on stderr. Delivery is recorded before exiting, so the wait started
// by the next turn finds nothing to deliver.
func wait(c *ctx) (any, int, error) {
	if !c.app.Cfg.AutoResume {
		return nil, 0, nil
	}
	sid := c.in.SessionID
	me := os.Getpid()
	deadline := time.Now().Add(waitLimit(c.getenv))
	poll := pollInterval(c.getenv)
	for first := true; ; first = false {
		var text string
		done := false
		err := c.app.Update(func(e *engine.Engine) error {
			s := e.St.Sessions[sid]
			if s == nil {
				done = true
				return nil
			}
			if first {
				s.WaiterPID = me
			} else if s.WaiterPID != me {
				done = true // a newer wait took over
				return nil
			}
			text = e.TakeDeliveries(sid)
			if text == "" && !e.Waiting(sid) {
				done = true
			}
			if text != "" || done {
				s.WaiterPID = 0
			}
			return nil
		})
		if err != nil {
			return nil, 0, err
		}
		if text != "" {
			c.app.Logf(sid, "wait", "delivered handoff, waking session")
			fmt.Fprintf(c.stderr, "%s\n\nContinue the work you were waiting to do.\n", text)
			return nil, 2, nil
		}
		if done || (c.pid > 0 && !proc.Alive(c.pid)) || time.Now().After(deadline) {
			return nil, 0, nil
		}
		time.Sleep(poll)
	}
}

func sessionEnd(c *ctx) (any, int, error) {
	sid := c.in.SessionID
	err := c.app.Update(func(e *engine.Engine) error {
		e.EndSession(sid)
		return nil
	})
	if err == nil {
		c.app.Logf(sid, "session-end", "released everything (%s)", c.in.Reason)
	}
	return nil, 0, err
}

func isInGitDir(repo gitx.Repo, path string) bool {
	return strings.HasPrefix(path, repo.CommonDir+string(filepath.Separator)) ||
		strings.Contains(path, string(filepath.Separator)+".git"+string(filepath.Separator))
}

// waitLimit stays under the hook's 3600 s timeout.
func waitLimit(getenv func(string) string) time.Duration {
	if s, err := strconv.Atoi(getenv("FILE_BATON_WAIT_SECONDS")); err == nil && s > 0 {
		return time.Duration(s) * time.Second
	}
	return 3500 * time.Second
}

func pollInterval(getenv func(string) string) time.Duration {
	if ms, err := strconv.Atoi(getenv("FILE_BATON_POLL_MS")); err == nil && ms > 0 {
		return time.Duration(ms) * time.Millisecond
	}
	return time.Second
}
