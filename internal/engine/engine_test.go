package engine

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/KaitouKid1412/file-baton/internal/config"
	"github.com/KaitouKid1412/file-baton/internal/state"
)

type fakeEnv struct {
	now      time.Time
	dead     map[string]bool
	activity map[string]time.Time
	files    map[string]string // current contents; absent = no file
	snaps    map[string]string
	clean    map[string]bool
	n        int
}

func newFakeEnv() *fakeEnv {
	return &fakeEnv{
		now:      time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC),
		dead:     map[string]bool{},
		activity: map[string]time.Time{},
		files:    map[string]string{},
		snaps:    map[string]string{},
		clean:    map[string]bool{},
	}
}

func (f *fakeEnv) Now() time.Time              { return f.now }
func (f *fakeEnv) Alive(s *state.Session) bool { return !f.dead[s.ID] }
func (f *fakeEnv) LastActivity(s *state.Session) time.Time {
	if t, ok := f.activity[s.ID]; ok {
		return t
	}
	return s.LastSeen
}
func (f *fakeEnv) Snapshot(path, sid string) string {
	content, ok := f.files[path]
	if !ok {
		return ""
	}
	f.n++
	name := fmt.Sprintf("snap%d", f.n)
	f.snaps[name] = content
	return name
}
func (f *fakeEnv) DropSnapshot(name string) { delete(f.snaps, name) }
func (f *fakeEnv) Diff(l *state.Lock) (string, bool) {
	before, hadBefore := f.snaps[l.Snapshot]
	after, hasAfter := f.files[l.Path]
	if hadBefore == hasAfter && before == after {
		return "", false
	}
	return fmt.Sprintf("-%s\n+%s", before, after), true
}
func (f *fakeEnv) IsClean(path string) bool { return f.clean[path] }

func newEngine(env *fakeEnv) *Engine {
	return &Engine{
		St:  state.New(),
		Env: env,
		Cfg: config.Default(),
		Rel: func(p string) string { return strings.TrimPrefix(p, "/repo/") },
		CLI: "/plugin/libexec/file-baton",
	}
}

const (
	fileA = "/repo/a.go"
	fileB = "/repo/b.go"
)

func mustAllow(t *testing.T, d Decision) {
	t.Helper()
	if d.Deny {
		t.Fatalf("expected allow, got deny:\n%s", d.Reason)
	}
}

func mustDeny(t *testing.T, d Decision, contains ...string) {
	t.Helper()
	if !d.Deny {
		t.Fatalf("expected deny, got allow")
	}
	for _, c := range contains {
		if !strings.Contains(d.Reason, c) {
			t.Fatalf("deny reason missing %q:\n%s", c, d.Reason)
		}
	}
}

func TestFreeFileIsGrantedAndReentrant(t *testing.T) {
	env := newFakeEnv()
	e := newEngine(env)
	e.Seen("A", 1, "")
	mustAllow(t, e.Acquire("A", fileA))
	mustAllow(t, e.Acquire("A", fileA))
	if l := e.St.Locks[fileA]; l.Owner != "A" || l.Status != state.Held {
		t.Fatalf("lock = %+v", l)
	}
}

func TestBlockedSessionIsQueuedOnce(t *testing.T) {
	env := newFakeEnv()
	e := newEngine(env)
	e.Seen("A", 1, "")
	e.SetTask("A", "add   pagination\nto the list endpoint")
	e.Seen("B", 2, "")
	e.Seen("C", 3, "")
	mustAllow(t, e.Acquire("A", fileA))
	mustDeny(t, e.Acquire("B", fileA), "a.go is busy: another Claude session (A) is editing it", "expected, not a failure", `"add pagination to the list endpoint"`, "#1 in line", "woken up")
	mustDeny(t, e.Acquire("C", fileA), "#2 in line")
	mustDeny(t, e.Acquire("B", fileA), "#1 in line")
	if q := e.St.Locks[fileA].Queue; len(q) != 2 {
		t.Fatalf("queue = %+v", q)
	}
}

func TestEndTurnHandsOffWithDiffAndNotesOnce(t *testing.T) {
	env := newFakeEnv()
	env.files[fileA] = "old"
	e := newEngine(env)
	e.Seen("A", 1, "")
	e.SetTask("A", "rename things")
	e.Seen("B", 2, "")
	mustAllow(t, e.Acquire("A", fileA))
	mustDeny(t, e.Acquire("B", fileA))
	env.files[fileA] = "new"
	if err := e.AddNote("A", fileA, "renamed foo to bar"); err != nil {
		t.Fatal(err)
	}
	if err := e.AddNote("B", fileA, "not mine"); err == nil {
		t.Fatal("B should not be able to note a file it does not hold")
	}
	e.EndTurn("A")

	l := e.St.Locks[fileA]
	if l.Owner != "B" || l.Status != state.Granted || l.Handoff == nil {
		t.Fatalf("lock after handoff = %+v", l)
	}
	if got := e.St.Touched[fileA]; len(got) != 1 || got[0].Session != "A" {
		t.Fatalf("touched = %+v", got)
	}
	mustDeny(t, e.Acquire("B", fileA), "you now hold a.go", "finished with it", `"rename things"`, "renamed foo to bar", "-old\n+new", "Re-read the file")
	mustAllow(t, e.Acquire("B", fileA))
	if e.Waiting("B") {
		t.Fatal("B should no longer be waiting")
	}
}

func TestTakeDeliveriesIsExactlyOnce(t *testing.T) {
	env := newFakeEnv()
	e := newEngine(env)
	e.Seen("A", 1, "")
	e.Seen("B", 2, "")
	mustAllow(t, e.Acquire("A", fileA))
	mustDeny(t, e.Acquire("B", fileA))
	if e.TakeDeliveries("B") != "" {
		t.Fatal("nothing should be delivered while A holds the file")
	}
	if !e.Waiting("B") {
		t.Fatal("B should be waiting")
	}
	e.EndTurn("A")
	if text := e.TakeDeliveries("B"); !strings.Contains(text, "you now hold a.go") {
		t.Fatalf("delivery = %q", text)
	}
	if text := e.TakeDeliveries("B"); text != "" {
		t.Fatalf("second delivery = %q", text)
	}
	if e.Waiting("B") {
		t.Fatal("B should not be waiting after delivery")
	}
	mustAllow(t, e.Acquire("B", fileA))
}

func TestGrantedLockSurvivesTheGranteesTurnEnd(t *testing.T) {
	env := newFakeEnv()
	e := newEngine(env)
	e.Seen("A", 1, "")
	e.Seen("B", 2, "")
	mustAllow(t, e.Acquire("A", fileA))
	mustDeny(t, e.Acquire("B", fileA))
	e.EndTurn("A")
	e.EndTurn("B") // B's turn ends before it picked the file up
	if l := e.St.Locks[fileA]; l == nil || l.Owner != "B" || l.Status != state.Granted {
		t.Fatalf("grant was lost: %+v", l)
	}
}

func TestCrashedHolderIsReaped(t *testing.T) {
	env := newFakeEnv()
	env.files[fileA] = "v1"
	e := newEngine(env)
	e.Seen("A", 1, "")
	e.Seen("B", 2, "")
	mustAllow(t, e.Acquire("A", fileA))
	mustDeny(t, e.Acquire("B", fileA))
	env.files[fileA] = "v2"
	env.dead["A"] = true
	e.Reap()
	if e.St.Sessions["A"] != nil {
		t.Fatal("dead session should be removed")
	}
	mustDeny(t, e.Acquire("B", fileA), "stopped running", "-v1\n+v2")
	mustAllow(t, e.Acquire("B", fileA))
}

func TestDeadWaiterIsSkipped(t *testing.T) {
	env := newFakeEnv()
	e := newEngine(env)
	e.Seen("A", 1, "")
	e.Seen("B", 2, "")
	e.Seen("C", 3, "")
	mustAllow(t, e.Acquire("A", fileA))
	mustDeny(t, e.Acquire("B", fileA))
	mustDeny(t, e.Acquire("C", fileA))
	env.dead["B"] = true
	e.EndTurn("A")
	if l := e.St.Locks[fileA]; l.Owner != "C" {
		t.Fatalf("lock should skip dead B and go to C: %+v", l)
	}
}

func TestIdleHolderIsReleased(t *testing.T) {
	env := newFakeEnv()
	e := newEngine(env)
	e.Seen("A", 1, "")
	e.Seen("B", 2, "")
	mustAllow(t, e.Acquire("A", fileA))
	mustDeny(t, e.Acquire("B", fileA))
	env.activity["A"] = env.now
	env.activity["B"] = env.now.Add(30 * time.Minute)
	env.now = env.now.Add(30 * time.Minute)
	e.Reap()
	l := e.St.Locks[fileA]
	if l.Owner != "B" || l.Handoff.Reason != ReasonIdle {
		t.Fatalf("lock = %+v handoff = %+v", l, l.Handoff)
	}
}

func TestIgnoredGrantPassesOnAndRequeues(t *testing.T) {
	env := newFakeEnv()
	e := newEngine(env)
	for _, s := range []string{"A", "B", "C"} {
		e.Seen(s, 1, "")
	}
	mustAllow(t, e.Acquire("A", fileA))
	mustDeny(t, e.Acquire("B", fileA))
	e.EndTurn("A")
	// B ignores the grant; a lone grant stays reserved however long it waits.
	env.now = env.now.Add(time.Hour)
	e.Reap()
	if e.St.Locks[fileA].Owner != "B" {
		t.Fatal("a grant nobody else wants should stay with B")
	}
	// C arrives: the stale grant moves to C, B goes to the back of the line.
	mustDeny(t, e.Acquire("C", fileA), "you now hold a.go")
	l := e.St.Locks[fileA]
	if l.Owner != "C" || l.Status != state.Held || len(l.Queue) != 1 || l.Queue[0].Session != "B" {
		t.Fatalf("lock = %+v", l)
	}
}

func TestEndSessionLeavesEverything(t *testing.T) {
	env := newFakeEnv()
	e := newEngine(env)
	for _, s := range []string{"A", "B", "C"} {
		e.Seen(s, 1, "")
	}
	mustAllow(t, e.Acquire("A", fileA))
	mustAllow(t, e.Acquire("B", fileB))
	mustDeny(t, e.Acquire("B", fileA))
	mustDeny(t, e.Acquire("C", fileB))
	e.EndSession("B")
	if e.St.Sessions["B"] != nil {
		t.Fatal("session should be gone")
	}
	if q := e.St.Locks[fileA].Queue; len(q) != 0 {
		t.Fatalf("B should have left a.go's queue: %+v", q)
	}
	if l := e.St.Locks[fileB]; l.Owner != "C" || l.Handoff.Reason != ReasonEnded {
		t.Fatalf("b.go should go to C: %+v", l)
	}
}

func TestHeadsUpIsGivenOncePerWaiter(t *testing.T) {
	env := newFakeEnv()
	e := newEngine(env)
	e.Seen("A", 1, "")
	e.Seen("B", 2, "")
	e.Seen("C", 3, "")
	mustAllow(t, e.Acquire("A", fileA))
	if e.HeadsUp("A") != "" {
		t.Fatal("nobody waits yet")
	}
	mustDeny(t, e.Acquire("B", fileA))
	if h := e.HeadsUp("A"); !strings.Contains(h, `"/plugin/libexec/file-baton" note a.go`) || !strings.Contains(h, "released automatically") {
		t.Fatalf("heads-up = %q", h)
	}
	if e.HeadsUp("A") != "" {
		t.Fatal("heads-up repeated for the same waiter")
	}
	mustDeny(t, e.Acquire("C", fileA))
	if e.HeadsUp("A") == "" {
		t.Fatal("a new waiter should produce a new heads-up")
	}
}

func TestNotificationsAreNotTasks(t *testing.T) {
	env := newFakeEnv()
	e := newEngine(env)
	e.Seen("A", 1, "")
	e.SetTask("A", "add validation")
	e.SetTask("A", "<task-notification>\n<summary>Stop hook feedback</summary>\n</task-notification>\nfile-baton: you now hold a.go")
	e.SetTask("A", `<cross-session-message from="uds:/tmp/cc-socks/22822.sock" from-name="mantle-01" from-mode="prompting"> From mantle-01: please confirm these shapes`)
	if got := e.St.Sessions["A"].Task; got != "add validation" {
		t.Fatalf("task = %q", got)
	}
}

func TestForeignChangesAndPruning(t *testing.T) {
	env := newFakeEnv()
	env.files[fileA] = "v1"
	e := newEngine(env)
	e.Seen("A", 1, "")
	e.SetTask("A", "task A")
	e.Seen("B", 2, "")
	mustAllow(t, e.Acquire("A", fileA))
	env.files[fileA] = "v2"
	e.EndTurn("A")
	mustAllow(t, e.Acquire("A", fileB)) // A is still editing b.go

	all := func(string) bool { return true }
	got := e.ForeignChanges("B", all)
	if len(got) != 2 || got[0].Path != fileA || got[0].Task != "task A" || got[1].Path != fileB {
		t.Fatalf("foreign = %+v", got)
	}
	if len(e.ForeignChanges("A", all)) != 0 {
		t.Fatal("A's own changes are not foreign to A")
	}
	e.EndSession("A")
	got = e.ForeignChanges("B", all)
	if len(got) != 1 || !got[0].Ended {
		t.Fatalf("after A ended: %+v", got)
	}
	env.clean[fileA] = true
	e.PruneTouched()
	if len(e.ForeignChanges("B", all)) != 0 {
		t.Fatal("clean files should be pruned")
	}
}

func TestChangeCommittedDuringTheTurnIsNotRecorded(t *testing.T) {
	env := newFakeEnv()
	env.files[fileA] = "v1"
	e := newEngine(env)
	e.Seen("A", 1, "")
	mustAllow(t, e.Acquire("A", fileA))
	env.files[fileA] = "v2"
	env.clean[fileA] = true // A committed its change before the turn ended
	e.EndTurn("A")
	if got := e.St.Touched[fileA]; len(got) != 0 {
		t.Fatalf("committed change recorded as uncommitted: %+v", got)
	}
}

func TestCommittedFilesAreReleasedAndHandedOn(t *testing.T) {
	env := newFakeEnv()
	env.files[fileA], env.files[fileB] = "a1", "b1"
	e := newEngine(env)
	e.Seen("A", 1, "")
	e.SetTask("A", "task A")
	e.Seen("B", 2, "")
	mustAllow(t, e.Acquire("A", fileA))
	mustAllow(t, e.Acquire("A", fileB))
	mustDeny(t, e.Acquire("B", fileA))
	env.files[fileA], env.files[fileB] = "a2", "b2"
	env.clean[fileA] = true // committed; b.go still has uncommitted changes

	released, handedOn := e.ReleaseCommitted("A")
	if len(released) != 1 || released[0] != fileA || len(handedOn) != 1 || handedOn[0] != fileA {
		t.Fatalf("released = %v, handedOn = %v", released, handedOn)
	}
	if l := e.St.Locks[fileB]; l == nil || l.Owner != "A" || l.Status != state.Held {
		t.Fatalf("uncommitted b.go should stay held: %+v", l)
	}
	if len(e.St.Touched[fileA]) != 0 {
		t.Fatal("a committed file is nobody's uncommitted work")
	}
	mustDeny(t, e.Acquire("B", fileA), "committed its changes to it", `"task A"`, "-a1\n+a2")
	mustAllow(t, e.Acquire("B", fileA))
	if text := e.CommittedText([]string{"a.go"}); !strings.Contains(text, "you committed a.go") {
		t.Fatalf("committed text = %q", text)
	}
}

func TestUntouchedHoldExpiresMidTurn(t *testing.T) {
	env := newFakeEnv()
	e := newEngine(env)
	e.Seen("A", 1, "")
	e.Seen("B", 2, "")
	mustAllow(t, e.Acquire("A", fileA))
	mustDeny(t, e.Acquire("B", fileA))

	env.now = env.now.Add(8 * time.Minute)
	mustAllow(t, e.Acquire("A", fileA)) // editing again refreshes the hold
	env.now = env.now.Add(8 * time.Minute)
	env.activity["A"], env.activity["B"] = env.now, env.now // both sessions are busy
	e.Reap()
	if e.St.Locks[fileA].Owner != "A" {
		t.Fatal("8 minutes since the last edit is within the limit")
	}
	env.now = env.now.Add(3 * time.Minute)
	env.activity["A"], env.activity["B"] = env.now, env.now
	e.Reap()
	l := e.St.Locks[fileA]
	if l.Owner != "B" || l.Handoff.Reason != ReasonExpired {
		t.Fatalf("lock = %+v", l)
	}
	mustDeny(t, e.Acquire("B", fileA), "has not edited it for 10 minutes")
}

func TestHoldLimitCanBeTurnedOff(t *testing.T) {
	env := newFakeEnv()
	e := newEngine(env)
	e.Cfg.HoldTimeout = 0
	e.Seen("A", 1, "")
	mustAllow(t, e.Acquire("A", fileA))
	env.now = env.now.Add(time.Hour)
	env.activity["A"] = env.now
	e.Reap()
	if l := e.St.Locks[fileA]; l == nil || l.Owner != "A" {
		t.Fatalf("hold limit is off, lock = %+v", l)
	}
}

func TestLockFromV01WithoutLastEditUsesSince(t *testing.T) {
	env := newFakeEnv()
	e := newEngine(env)
	e.Seen("A", 1, "")
	e.St.Locks[fileA] = &state.Lock{Path: fileA, Owner: "A", Status: state.Held, Since: env.now}
	env.now = env.now.Add(5 * time.Minute)
	env.activity["A"] = env.now
	e.Reap()
	if e.St.Locks[fileA] == nil {
		t.Fatal("a v0.1 lock must not expire at once just because LastEdit is unset")
	}
}

func TestForcedReleaseHandsOn(t *testing.T) {
	env := newFakeEnv()
	e := newEngine(env)
	e.Seen("A", 1, "")
	e.Seen("B", 2, "")
	mustAllow(t, e.Acquire("A", fileA))
	mustDeny(t, e.Acquire("B", fileA))
	if !e.Release(fileA) {
		t.Fatal("release should find the lock")
	}
	mustDeny(t, e.Acquire("B", fileA), "The user released it")
	if e.Release(fileB) {
		t.Fatal("nothing to release")
	}
}

func TestNewFileHandoffShowsCreation(t *testing.T) {
	env := newFakeEnv()
	e := newEngine(env)
	e.Seen("A", 1, "")
	e.Seen("B", 2, "")
	mustAllow(t, e.Acquire("A", fileA)) // file does not exist yet
	mustDeny(t, e.Acquire("B", fileA))
	env.files[fileA] = "created"
	e.EndTurn("A")
	mustDeny(t, e.Acquire("B", fileA), "+created")
}
