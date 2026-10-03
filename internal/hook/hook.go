// Package hook implements file-baton's Claude Code hook handlers. Each handler
// reads the hook's JSON from stdin and answers on stdout. Any internal failure
// is logged and answered with nothing, so file-baton never blocks Claude by
// accident.
package hook

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"

	"github.com/KaitouKid1412/file-baton/internal/app"
	"github.com/KaitouKid1412/file-baton/internal/gitx"
)

// Input is the part of a hook's stdin file-baton reads.
type Input struct {
	SessionID      string          `json:"session_id"`
	TranscriptPath string          `json:"transcript_path"`
	Cwd            string          `json:"cwd"`
	HookEventName  string          `json:"hook_event_name"`
	ToolName       string          `json:"tool_name"`
	ToolInput      json.RawMessage `json:"tool_input"`
	Prompt         string          `json:"prompt"`
	StopHookActive bool            `json:"stop_hook_active"`
	AgentID        string          `json:"agent_id"`
	Reason         string          `json:"reason"`
}

// ctx is one hook invocation.
type ctx struct {
	in     Input
	app    *app.App
	getenv func(string) string
	stdout io.Writer
	stderr io.Writer
	pid    int
}

type handler func(c *ctx) (any, int, error)

var handlers = map[string]handler{
	"session-start": sessionStart,
	"prompt":        prompt,
	"pre-edit":      preEdit,
	"pre-bash":      preBash,
	"stop":          stop,
	"wait":          wait,
	"session-end":   sessionEnd,
}

// Events lists the hook events file-baton handles.
func Events() []string {
	return []string{"session-start", "prompt", "pre-edit", "pre-bash", "stop", "wait", "session-end"}
}

// Run handles one hook event and returns the process exit code.
func Run(event string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) (code int) {
	h, ok := handlers[event]
	if !ok {
		fmt.Fprintf(stderr, "file-baton: unknown hook event %q\n", event)
		return 0
	}
	c := &ctx{getenv: getenv, stdout: stdout, stderr: stderr, pid: app.PID(getenv)}
	defer func() {
		if r := recover(); r != nil {
			if c.app != nil {
				c.app.Logf(c.in.SessionID, event, "panic: %v\n%s", r, debug.Stack())
			}
			code = 0
		}
	}()
	data, err := io.ReadAll(stdin)
	if err != nil || json.Unmarshal(data, &c.in) != nil || c.in.SessionID == "" {
		return 0
	}
	dir := c.in.Cwd
	if dir == "" {
		dir, _ = os.Getwd()
	}
	c.app, err = app.Open(dir, getenv)
	if err != nil {
		return 0 // not a git work tree, or the store is unusable: stay out of the way
	}
	if c.app.Cfg.Disabled {
		return 0
	}
	out, code, err := h(c)
	if err != nil {
		c.app.Logf(c.in.SessionID, event, "error (allowed): %v", err)
		return 0
	}
	if out != nil {
		if err := json.NewEncoder(stdout).Encode(out); err != nil {
			return 0
		}
	}
	return code
}

// resolvePath turns a tool's file argument into the absolute, symlink-free
// path locks are keyed by.
func (c *ctx) resolvePath(p string) string {
	if !filepath.IsAbs(p) {
		p = filepath.Join(c.in.Cwd, p)
	}
	return gitx.Resolve(p)
}

// Output shapes.

type preToolUse struct {
	HookSpecificOutput preToolUseFields `json:"hookSpecificOutput"`
}

type preToolUseFields struct {
	HookEventName            string `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
	AdditionalContext        string `json:"additionalContext,omitempty"`
}

func deny(reason string) any {
	return preToolUse{preToolUseFields{HookEventName: "PreToolUse", PermissionDecision: "deny", PermissionDecisionReason: reason}}
}

func allow(reason, context string) any {
	return preToolUse{preToolUseFields{HookEventName: "PreToolUse", PermissionDecision: "allow", PermissionDecisionReason: reason, AdditionalContext: context}}
}

func addContext(context string) any {
	if context == "" {
		return nil
	}
	return preToolUse{preToolUseFields{HookEventName: "PreToolUse", AdditionalContext: context}}
}

type stopBlock struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}
