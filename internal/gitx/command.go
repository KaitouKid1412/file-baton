package gitx

import (
	"path/filepath"
	"strings"
)

// CommitPlan is what one `git commit` in a shell command would include,
// together with the `git add` calls that run before it in the same command.
type CommitPlan struct {
	Dir       string   // -C directory of the commit, "" for the current one
	All       bool     // -a / --all
	Paths     []string // pathspecs given to the commit
	AddAll    bool     // an earlier git add -A / --all / .
	AddUpdate bool     // an earlier git add -u
	AddPaths  []string // paths given to earlier git add calls
	Allowed   bool     // FILE_BATON_ALLOW=1 prefix
}

// IsPlain reports whether nothing beyond the index is involved.
func (p CommitPlan) IsPlain() bool {
	return !p.All && len(p.Paths) == 0 && !p.AddAll && !p.AddUpdate && len(p.AddPaths) == 0
}

// ParseCommits finds every git commit in a shell command. It is a best-effort
// reading of common shapes, not a shell parser.
func ParseCommits(command string) []CommitPlan {
	var plans []CommitPlan
	var pending CommitPlan // git add effects seen so far
	for _, seg := range splitSegments(command) {
		allowed, dir, sub, args := parseGit(seg)
		switch sub {
		case "add":
			addAll, addUpdate, paths := parseAdd(args)
			pending.AddAll = pending.AddAll || addAll
			pending.AddUpdate = pending.AddUpdate || addUpdate
			pending.AddPaths = append(pending.AddPaths, joinAll(dir, paths)...)
		case "commit":
			plan := pending
			plan.AddPaths = append([]string(nil), pending.AddPaths...)
			plan.Dir = dir
			plan.Allowed = allowed
			plan.All, plan.Paths = parseCommit(args)
			plans = append(plans, plan)
		}
	}
	return plans
}

// parseGit reads `[VAR=value ...] git [global options] <sub> [args]`.
func parseGit(tokens []string) (allowed bool, dir, sub string, args []string) {
	i := 0
	for i < len(tokens) && isAssignment(tokens[i]) {
		if tokens[i] == "FILE_BATON_ALLOW=1" {
			allowed = true
		}
		i++
	}
	if i < len(tokens) && (tokens[i] == "env" || tokens[i] == "command") {
		i++
		for i < len(tokens) && isAssignment(tokens[i]) {
			if tokens[i] == "FILE_BATON_ALLOW=1" {
				allowed = true
			}
			i++
		}
	}
	if i >= len(tokens) || (tokens[i] != "git" && !strings.HasSuffix(tokens[i], "/git")) {
		return false, "", "", nil
	}
	i++
	for i < len(tokens) {
		t := tokens[i]
		switch {
		case t == "-C" && i+1 < len(tokens):
			if dir == "" {
				dir = tokens[i+1]
			} else {
				dir = filepath.Join(dir, tokens[i+1])
			}
			i += 2
		case (t == "-c" || t == "--git-dir" || t == "--work-tree" || t == "--namespace") && i+1 < len(tokens):
			i += 2
		case strings.HasPrefix(t, "-"):
			i++
		default:
			return allowed, dir, t, tokens[i+1:]
		}
	}
	return false, "", "", nil
}

var commitValueLong = map[string]bool{
	"--message": true, "--file": true, "--author": true, "--date": true,
	"--reuse-message": true, "--reedit-message": true, "--fixup": true, "--squash": true,
	"--template": true, "--cleanup": true, "--trailer": true, "--pathspec-from-file": true,
}

func parseCommit(args []string) (all bool, paths []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return all, append(paths, args[i+1:]...)
		case a == "--all":
			all = true
		case strings.HasPrefix(a, "--"):
			if !strings.Contains(a, "=") && commitValueLong[a] {
				i++
			}
		case strings.HasPrefix(a, "-") && len(a) > 1:
			for j := 1; j < len(a); j++ {
				c := a[j]
				if c == 'a' {
					all = true
				}
				if strings.IndexByte("mFCct", c) >= 0 {
					if j == len(a)-1 {
						i++ // the value is the next argument
					}
					break
				}
			}
		default:
			paths = append(paths, a)
		}
	}
	return all, paths
}

func parseAdd(args []string) (all, update bool, paths []string) {
	for i, a := range args {
		switch {
		case a == "--":
			for _, p := range args[i+1:] {
				if p == "." || p == ":/" {
					all = true
				} else {
					paths = append(paths, p)
				}
			}
			return all, update, paths
		case a == "-A" || a == "--all" || a == "." || a == ":/" || a == "--no-ignore-removal":
			all = true
		case a == "-u" || a == "--update":
			update = true
		case strings.HasPrefix(a, "-"):
			if len(a) > 1 && a[1] != '-' && strings.ContainsRune(a, 'A') {
				all = true
			} else if len(a) > 1 && a[1] != '-' && strings.ContainsRune(a, 'u') {
				update = true
			}
		default:
			paths = append(paths, a)
		}
	}
	return all, update, paths
}

func joinAll(dir string, paths []string) []string {
	if dir == "" {
		return paths
	}
	out := make([]string, len(paths))
	for i, p := range paths {
		if filepath.IsAbs(p) {
			out[i] = p
		} else {
			out[i] = filepath.Join(dir, p)
		}
	}
	return out
}

func isAssignment(t string) bool {
	eq := strings.IndexByte(t, '=')
	if eq <= 0 {
		return false
	}
	for i, c := range t[:eq] {
		if !(c == '_' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// Segments splits a shell command into simple commands on ;, &, |, newlines
// and parentheses, honouring quotes and backslashes, and returns each as its
// words.
func Segments(command string) [][]string { return splitSegments(command) }

func splitSegments(command string) [][]string {
	var segments [][]string
	var words []string
	var word strings.Builder
	inWord := false
	endWord := func() {
		if inWord {
			words = append(words, word.String())
			word.Reset()
			inWord = false
		}
	}
	endSegment := func() {
		endWord()
		if len(words) > 0 {
			segments = append(segments, words)
			words = nil
		}
	}
	runes := []rune(command)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch {
		case c == '\'':
			inWord = true
			for i++; i < len(runes) && runes[i] != '\''; i++ {
				word.WriteRune(runes[i])
			}
		case c == '"':
			inWord = true
			for i++; i < len(runes) && runes[i] != '"'; i++ {
				if runes[i] == '\\' && i+1 < len(runes) && strings.ContainsRune(`"\$`+"`", runes[i+1]) {
					i++
				}
				word.WriteRune(runes[i])
			}
		case c == '\\' && i+1 < len(runes):
			i++
			if runes[i] != '\n' {
				inWord = true
				word.WriteRune(runes[i])
			}
		case c == ';' || c == '&' || c == '|' || c == '\n' || c == '(' || c == ')':
			endSegment()
		case c == ' ' || c == '\t':
			endWord()
		default:
			inWord = true
			word.WriteRune(c)
		}
	}
	endSegment()
	return segments
}
