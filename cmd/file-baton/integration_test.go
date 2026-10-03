package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// These tests drive the real binary the way Claude Code does: one process per
// hook, JSON on stdin, sessions told apart by session_id and CLAUDE_PID.

var binary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "file-baton-bin")
	if err != nil {
		panic(err)
	}
	binary = filepath.Join(dir, "file-baton")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		panic(string(out))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type repo struct {
	t   *testing.T
	dir string
}

func newRepo(t *testing.T) *repo {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := &repo{t: t, dir: dir}
	r.git("init", "-q", "-b", "main")
	r.git("config", "user.email", "test@example.com")
	r.git("config", "user.name", "test")
	r.write("a.go", "package a\n\nfunc A() {}\n")
	r.write("b.go", "package a\n\nfunc B() {}\n")
	r.git("add", "-A")
	r.git("commit", "-q", "-m", "init")
	return r
}

func (r *repo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", r.dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func (r *repo) write(name, content string) {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.dir, name), []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *repo) path(name string) string { return filepath.Join(r.dir, name) }

// session is a fake Claude session.
type session struct {
	r   *repo
	id  string
	pid int
}

func (r *repo) session(id string, pid int) *session { return &session{r: r, id: id, pid: pid} }

type result struct {
	stdout, stderr string
	code           int
}

type hookOutput struct {
	HookSpecificOutput struct {
		PermissionDecision       string `json:"permissionDecision"`
		PermissionDecisionReason string `json:"permissionDecisionReason"`
		AdditionalContext        string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

func (res result) parsed() hookOutput {
	var out hookOutput
	_ = json.Unmarshal([]byte(res.stdout), &out)
	return out
}

// decision is "allow", "deny", "block" or "" (no decision).
func (res result) decision() string {
	out := res.parsed()
	if out.Decision != "" {
		return out.Decision
	}
	return out.HookSpecificOutput.PermissionDecision
}

// text is everything the hook told Claude.
func (res result) text() string {
	out := res.parsed()
	h := out.HookSpecificOutput
	return strings.Join([]string{h.PermissionDecisionReason, h.AdditionalContext, out.Reason}, "\n")
}

func baseEnv(extra ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "CLAUDE") || strings.HasPrefix(kv, "FILE_BATON_") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, extra...)
}

func (s *session) env(extra ...string) []string {
	return baseEnv(append([]string{"CLAUDE_PID=" + strconv.Itoa(s.pid), "CLAUDE_CODE_SESSION_ID=" + s.id}, extra...)...)
}

func (s *session) hook(event string, fields map[string]any, extra ...string) result {
	s.r.t.Helper()
	in := map[string]any{"session_id": s.id, "cwd": s.r.dir, "hook_event_name": event}
	for k, v := range fields {
		in[k] = v
	}
	data, _ := json.Marshal(in)
	cmd := exec.Command(binary, "hook", event)
	cmd.Dir = s.r.dir
	cmd.Env = s.env(extra...)
	cmd.Stdin = bytes.NewReader(data)
	return execCmd(s.r.t, cmd)
}

func (s *session) edit(name string) result {
	return s.hook("pre-edit", map[string]any{"tool_name": "Edit", "tool_input": map[string]any{"file_path": s.r.path(name)}})
}

func (s *session) bash(command string) result {
	return s.hook("pre-bash", map[string]any{"tool_name": "Bash", "tool_input": map[string]any{"command": command}})
}

func (s *session) stop() result { return s.hook("stop", nil) }

func (s *session) cli(args ...string) result {
	s.r.t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Dir = s.r.dir
	cmd.Env = s.env()
	return execCmd(s.r.t, cmd)
}

func execCmd(t *testing.T, cmd *exec.Cmd) result {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return result{stdout.String(), stderr.String(), code}
}

func expect(t *testing.T, res result, decision string, contains ...string) {
	t.Helper()
	if got := res.decision(); got != decision {
		t.Fatalf("decision = %q, want %q\nstdout: %s\nstderr: %s", got, decision, res.stdout, res.stderr)
	}
	for _, c := range contains {
		if !strings.Contains(res.text(), c) {
			t.Fatalf("output missing %q:\n%s", c, res.text())
		}
	}
}

// deadPID returns the pid of a process that has already exited.
func deadPID(t *testing.T) int {
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

func TestTwoSessionsAreSerializedWithHandoff(t *testing.T) {
	r := newRepo(t)
	a := r.session("aaaaaaaa-1111", os.Getpid())
	b := r.session("bbbbbbbb-2222", os.Getpid())
	a.hook("prompt", map[string]any{"prompt": "add a doc comment to A"})

	expect(t, a.edit("a.go"), "")
	expect(t, b.edit("a.go"), "deny", "being edited by another Claude session (aaaaaaaa)", "add a doc comment to A", "#1 in line")

	// A changes the file, then its next tool call carries the heads-up.
	r.write("a.go", "package a\n\n// A does nothing.\nfunc A() {}\n")
	expect(t, a.bash("go build ./..."), "", "waiting for a.go")
	if res := a.cli("note", "a.go", "added", "a", "doc", "comment"); res.code != 0 {
		t.Fatalf("note failed: %+v", res)
	}
	if res := b.cli("note", "a.go", "not mine"); res.code != 1 || !strings.Contains(res.stderr, "do not hold") {
		t.Fatalf("B's note should fail: %+v", res)
	}

	expect(t, a.stop(), "") // notes exist, so the turn ends and the file is handed on
	expect(t, b.edit("a.go"), "deny", "you now hold a.go", "added a doc comment", "+// A does nothing.", "Re-read the file")
	expect(t, b.edit("a.go"), "")
	expect(t, a.edit("a.go"), "deny", "being edited by another Claude session (bbbbbbbb)")
}

func TestStopAsksForNotesOnceWhenSomeoneWaits(t *testing.T) {
	r := newRepo(t)
	a := r.session("aaaaaaaa", os.Getpid())
	b := r.session("bbbbbbbb", os.Getpid())
	expect(t, a.edit("a.go"), "")
	expect(t, b.edit("a.go"), "deny")
	expect(t, a.stop(), "block", "waiting for files you edited: a.go")
	expect(t, a.stop(), "")
	expect(t, b.edit("a.go"), "deny", "you now hold a.go")
}

func TestWaiterWakesTheWaitingSessionExactlyOnce(t *testing.T) {
	r := newRepo(t)
	a := r.session("aaaaaaaa", os.Getpid())
	b := r.session("bbbbbbbb", os.Getpid())
	expect(t, a.edit("a.go"), "")
	expect(t, b.edit("a.go"), "deny")
	expect(t, b.stop(), "") // B's turn ends while it waits

	done := make(chan result, 1)
	go func() { done <- b.hook("wait", nil, "FILE_BATON_POLL_MS=50", "FILE_BATON_WAIT_SECONDS=20") }()
	time.Sleep(300 * time.Millisecond)
	select {
	case res := <-done:
		t.Fatalf("waiter returned before the file was free: %+v", res)
	default:
	}

	expect(t, a.stop(), "block") // asks A for notes first
	expect(t, a.stop(), "")
	res := <-done
	if res.code != 2 || !strings.Contains(res.stderr, "you now hold a.go") || !strings.Contains(res.stderr, "Continue the work") {
		t.Fatalf("waiter result = %+v", res)
	}
	// The next turn's waiter has nothing to deliver and returns at once.
	if res := b.hook("wait", nil, "FILE_BATON_POLL_MS=50"); res.code != 0 {
		t.Fatalf("second waiter = %+v", res)
	}
	expect(t, b.edit("a.go"), "") // delivered by the waiter, so no second deny
}

func TestNewerWaiterSupersedesOlder(t *testing.T) {
	r := newRepo(t)
	a := r.session("aaaaaaaa", os.Getpid())
	b := r.session("bbbbbbbb", os.Getpid())
	expect(t, a.edit("a.go"), "")
	expect(t, b.edit("a.go"), "deny")
	first := make(chan result, 1)
	go func() { first <- b.hook("wait", nil, "FILE_BATON_POLL_MS=50", "FILE_BATON_WAIT_SECONDS=20") }()
	time.Sleep(300 * time.Millisecond)
	second := make(chan result, 1)
	go func() { second <- b.hook("wait", nil, "FILE_BATON_POLL_MS=50", "FILE_BATON_WAIT_SECONDS=20") }()
	if res := <-first; res.code != 0 {
		t.Fatalf("older waiter should step aside: %+v", res)
	}
	expect(t, a.stop(), "block")
	expect(t, a.stop(), "")
	if res := <-second; res.code != 2 {
		t.Fatalf("newer waiter should deliver: %+v", res)
	}
}

func TestAutoResumeOffSkipsWaiter(t *testing.T) {
	r := newRepo(t)
	a := r.session("aaaaaaaa", os.Getpid())
	b := r.session("bbbbbbbb", os.Getpid())
	expect(t, a.edit("a.go"), "")
	expect(t, b.hook("pre-edit", map[string]any{"tool_input": map[string]any{"file_path": r.path("a.go")}}, "FILE_BATON_AUTO_RESUME=false"), "deny", "Retry this edit later")
	if res := b.hook("wait", nil, "FILE_BATON_AUTO_RESUME=false"); res.code != 0 {
		t.Fatalf("waiter should not run: %+v", res)
	}
}

func TestCrashedSessionLosesItsLocks(t *testing.T) {
	r := newRepo(t)
	a := r.session("aaaaaaaa", deadPID(t))
	b := r.session("bbbbbbbb", os.Getpid())
	// A registers while alive in spirit; its pid is already gone by B's attempt.
	expect(t, a.edit("a.go"), "")
	r.write("a.go", "package a\n\nfunc A() { panic(1) }\n")
	expect(t, b.edit("a.go"), "")
	expect(t, b.edit("a.go"), "")
	if out := b.cli("status"); strings.Contains(out.stdout, "aaaaaaaa  pid") {
		t.Fatalf("crashed session still listed:\n%s", out.stdout)
	}
}

func TestCommitGuard(t *testing.T) {
	r := newRepo(t)
	a := r.session("aaaaaaaa", os.Getpid())
	b := r.session("bbbbbbbb", os.Getpid())
	a.hook("prompt", map[string]any{"prompt": "change A"})

	expect(t, a.edit("a.go"), "")
	r.write("a.go", "package a\n\nfunc A() { println() }\n")
	expect(t, a.stop(), "")

	expect(t, b.edit("b.go"), "")
	r.write("b.go", "package a\n\nfunc B() { println() }\n")

	expect(t, b.bash(`git commit -am "B's change"`), "deny", "a.go  session aaaaaaaa", `"change A"`, "git restore --staged a.go")
	expect(t, b.bash(`git add -A && git commit -m "B"`), "deny", "a.go")
	expect(t, b.bash(`git commit -m "B" -- b.go`), "")
	expect(t, b.bash(`git commit -m "B"`), "") // nothing of A's is staged
	expect(t, b.bash(`FILE_BATON_ALLOW=1 git commit -am "everything"`), "")
	expect(t, a.bash(`git commit -am "A's change"`), "deny", "b.go  session bbbbbbbb") // B is still editing b.go

	r.git("commit", "-q", "-m", "A's change", "--", "a.go")
	expect(t, b.bash(`git commit -am "B's change"`), "") // A's change is committed: pruned
}

func TestOwnCLIIsAutoApproved(t *testing.T) {
	r := newRepo(t)
	a := r.session("aaaaaaaa", os.Getpid())
	cli := "/plugins/file-baton/libexec/file-baton"
	run := func(command string) result {
		return a.hook("pre-bash", map[string]any{"tool_input": map[string]any{"command": command}}, "CLAUDE_PLUGIN_ROOT=/plugins/file-baton")
	}
	expect(t, run(`"`+cli+`" note a.go "renamed A"`), "allow")
	expect(t, run(cli+` status`), "allow")
	expect(t, run(cli+` release --all`), "")
	expect(t, run(`"`+cli+`" note a.go "x"; rm -rf /`), "")
	expect(t, run(`"`+cli+`" note a.go "$(whoami)"`), "")
	expect(t, run(`/elsewhere/file-baton note a.go x`), "")
}

func TestOutsideRepoAndOutsideWorktreeAreIgnored(t *testing.T) {
	r := newRepo(t)
	a := r.session("aaaaaaaa", os.Getpid())
	b := r.session("bbbbbbbb", os.Getpid())
	outside := filepath.Join(t.TempDir(), "x.txt")
	expect(t, a.hook("pre-edit", map[string]any{"tool_input": map[string]any{"file_path": outside}}), "")
	expect(t, b.hook("pre-edit", map[string]any{"tool_input": map[string]any{"file_path": outside}}), "")

	plain := t.TempDir()
	cmd := exec.Command(binary, "hook", "pre-edit")
	cmd.Env = baseEnv("CLAUDE_PID=1")
	cmd.Stdin = strings.NewReader(`{"session_id":"x","cwd":"` + plain + `","tool_input":{"file_path":"` + plain + `/f"}}`)
	if res := execCmd(t, cmd); res.code != 0 || res.stdout != "" {
		t.Fatalf("non-repo: %+v", res)
	}
}

func TestSessionEndHandsOn(t *testing.T) {
	r := newRepo(t)
	a := r.session("aaaaaaaa", os.Getpid())
	b := r.session("bbbbbbbb", os.Getpid())
	expect(t, a.edit("a.go"), "")
	expect(t, b.edit("a.go"), "deny")
	a.hook("session-end", map[string]any{"reason": "prompt_input_exit"})
	expect(t, b.edit("a.go"), "deny", "Session aaaaaaaa exited")
	expect(t, b.edit("a.go"), "")
}

func TestReleaseAndStatusCommands(t *testing.T) {
	r := newRepo(t)
	a := r.session("aaaaaaaa", os.Getpid())
	b := r.session("bbbbbbbb", os.Getpid())
	expect(t, a.edit("a.go"), "")
	expect(t, b.edit("a.go"), "deny")
	st := b.cli("status")
	for _, want := range []string{"a.go  held by aaaaaaaa", "waiting: bbbbbbbb", "Sessions:"} {
		if !strings.Contains(st.stdout, want) {
			t.Fatalf("status missing %q:\n%s", want, st.stdout)
		}
	}
	if res := b.cli("release", "a.go"); res.code != 0 || !strings.Contains(res.stdout, "released a.go") {
		t.Fatalf("release: %+v", res)
	}
	expect(t, b.edit("a.go"), "deny", "The user released it")
	if res := b.cli("status", "--json"); !strings.Contains(res.stdout, `"owner": "bbbbbbbb"`) {
		t.Fatalf("status --json:\n%s", res.stdout)
	}
}
