package gitx

import (
	"reflect"
	"testing"
)

func TestParseCommits(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		want []CommitPlan
	}{
		{"not git", "ls -la && echo commit", nil},
		{"other subcommand", "git status", nil},
		{"plain", `git commit -m "fix: thing"`, []CommitPlan{{}}},
		{"all short", `git commit -am "msg"`, []CommitPlan{{All: true}}},
		{"all long", `git commit --all --message=msg`, []CommitPlan{{All: true}}},
		{"message value is not a path", `git commit -m msg`, []CommitPlan{{}}},
		{"message joined", `git commit -mmsg`, []CommitPlan{{}}},
		{"pathspecs", `git commit -m "x" -- a.go "dir/b c.go"`, []CommitPlan{{Paths: []string{"a.go", "dir/b c.go"}}}},
		{"bare pathspecs", `git commit -m x a.go b.go`, []CommitPlan{{Paths: []string{"a.go", "b.go"}}}},
		{"author value skipped", `git commit --author "A <a@b>" -m x`, []CommitPlan{{}}},
		{"global -C", `git -C sub commit -m x`, []CommitPlan{{Dir: "sub"}}},
		{"global -c and --no-pager", `git -c user.name=x --no-pager commit -m x`, []CommitPlan{{}}},
		{"add all then commit", `git add -A && git commit -m "x"`, []CommitPlan{{AddAll: true}}},
		{"add dot then commit", `git add . ; git commit -m x`, []CommitPlan{{AddAll: true}}},
		{"add update", `git add -u && git commit -m x`, []CommitPlan{{AddUpdate: true}}},
		{"add paths", `git add a.go b.go && git commit -m x`, []CommitPlan{{AddPaths: []string{"a.go", "b.go"}}}},
		{"override", `FILE_BATON_ALLOW=1 git commit -am x`, []CommitPlan{{All: true, Allowed: true}}},
		{"other env prefix", `GIT_AUTHOR_NAME=x git commit -m y`, []CommitPlan{{}}},
		{"env command", `env FILE_BATON_ALLOW=1 git commit -m y`, []CommitPlan{{Allowed: true}}},
		{"commit word inside quotes", `echo "git commit -am x"`, nil},
		{"multiline", "cd repo\ngit commit -am 'msg\nmore'", []CommitPlan{{All: true}}},
		{"subshell", `(cd sub && git commit -am x)`, []CommitPlan{{All: true}}},
		{"pipe", `git commit -m x | cat`, []CommitPlan{{}}},
		{"amend no edit", `git commit --amend --no-edit`, []CommitPlan{{}}},
		{"message file", `git commit -F msg.txt`, []CommitPlan{{}}},
		{"claude-style heredoc message", "git add a.go b.go && git commit -m \"$(cat <<'EOF'\nFix the thing\n\nCo-Authored-By: x <y@z>\nEOF\n)\"",
			[]CommitPlan{{AddPaths: []string{"a.go", "b.go"}}}},
		{"heredoc with -a", "git commit -a -m \"$(cat <<'EOF'\nmsg; with | chars && more\nEOF\n)\"", []CommitPlan{{All: true}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseCommits(tt.cmd)
			for i := range got {
				if len(got[i].AddPaths) == 0 {
					got[i].AddPaths = nil
				}
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ParseCommits(%q)\n got  %+v\n want %+v", tt.cmd, got, tt.want)
			}
		})
	}
}

func TestSegmentsHonourQuotes(t *testing.T) {
	got := Segments(`a "b ; c" 'd | e' f\ g; h`)
	want := [][]string{{"a", "b ; c", "d | e", "f g"}, {"h"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestFormatDiffRelabelsAndCaps(t *testing.T) {
	out := "diff --git a/x b/y\nindex 1..2\n--- a/tmp/snap\n+++ b/real\n@@ -1 +1 @@\n-a\n+b\n"
	if got, want := formatDiff(out, "src/x.go", 0), "--- a/src/x.go\n+++ b/src/x.go\n@@ -1 +1 @@\n-a\n+b"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if got, want := formatDiff(out, "x", 2), "--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n… 1 more diff lines not shown"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
