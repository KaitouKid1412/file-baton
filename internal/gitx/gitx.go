// Package gitx runs the few git commands file-baton needs.
package gitx

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ErrNoRepo means the directory is not inside a git work tree.
var ErrNoRepo = errors.New("not inside a git work tree")

// Repo is one work tree of a repository.
type Repo struct {
	Root      string // top level of the work tree, symlinks resolved
	CommonDir string // git common directory, shared by all work trees
}

// Discover finds the work tree containing dir.
func Discover(dir string) (Repo, error) {
	out, err := git(dir, "rev-parse", "--path-format=absolute", "--show-toplevel", "--git-common-dir")
	if err != nil {
		return Repo{}, ErrNoRepo
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 || lines[0] == "" {
		return Repo{}, ErrNoRepo
	}
	return Repo{Root: Resolve(lines[0]), CommonDir: lines[1]}, nil
}

// Contains reports whether the absolute path lies inside the work tree.
func (r Repo) Contains(path string) bool {
	return path == r.Root || strings.HasPrefix(path, r.Root+string(filepath.Separator))
}

// Rel returns path relative to the work tree root, or path itself when it lies
// outside the work tree.
func (r Repo) Rel(path string) string {
	if !r.Contains(path) {
		return path
	}
	rel, err := filepath.Rel(r.Root, path)
	if err != nil {
		return path
	}
	return rel
}

// Resolve makes a path absolute and resolves symlinks. For a file that does not
// exist yet, the nearest existing parent is resolved and the rest appended.
func Resolve(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	var rest []string
	cur := abs
	for {
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			parts := append([]string{real}, rest...)
			return filepath.Join(parts...)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs
		}
		rest = append([]string{filepath.Base(cur)}, rest...)
		cur = parent
	}
}

// SnapshotDiff describes the change from snapshot (a file path, "" when the
// file did not exist) to the file at path. rel labels the file in the output.
// The diff body is capped at maxLines lines.
func SnapshotDiff(snapshot, path, rel string, maxLines int) (string, bool) {
	before, errBefore := readOrNil(snapshot)
	after, errAfter := readOrNil(path)
	if errBefore != nil || errAfter != nil {
		return "(could not compare the file's contents)", true
	}
	if bytes.Equal(before, after) && (before == nil) == (after == nil) {
		return "", false
	}
	switch {
	case before == nil && after != nil:
		if isBinary(after) {
			return "(new binary file)", true
		}
	case after == nil:
		return "(file deleted)", true
	}
	if isBinary(before) || isBinary(after) {
		return "(binary file changed)", true
	}
	oldPath := snapshot
	if before == nil {
		oldPath = os.DevNull
	}
	cmd := exec.Command("git", "diff", "--no-index", "--no-color", "--no-ext-diff", "-U3", "--", oldPath, path)
	cmd.Env = gitEnv()
	out, err := cmd.Output()
	var exit *exec.ExitError
	if err != nil && !(errors.As(err, &exit) && exit.ExitCode() == 1) {
		return "(could not compute the diff)", true
	}
	return formatDiff(string(out), rel, maxLines), true
}

// formatDiff drops git's headers (they name the snapshot file), labels the
// file with rel and caps the body.
func formatDiff(out, rel string, maxLines int) string {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	start := 0
	for start < len(lines) && !strings.HasPrefix(lines[start], "@@") {
		start++
	}
	body := lines[start:]
	if len(body) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n", rel, rel)
	if maxLines > 0 && len(body) > maxLines {
		b.WriteString(strings.Join(body[:maxLines], "\n"))
		fmt.Fprintf(&b, "\n… %d more diff lines not shown", len(body)-maxLines)
		return b.String()
	}
	b.WriteString(strings.Join(body, "\n"))
	return b.String()
}

func readOrNil(path string) ([]byte, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if data == nil && err == nil {
		data = []byte{}
	}
	return data, err
}

func isBinary(data []byte) bool {
	n := min(len(data), 8000)
	return bytes.IndexByte(data[:n], 0) >= 0
}

// IsClean reports whether git sees no uncommitted change (staged, unstaged or
// untracked) in the file. Any error counts as not clean.
func IsClean(path string) bool {
	dir := filepath.Dir(path)
	for {
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
	out, err := git(dir, "status", "--porcelain", "--untracked-files=all", "--", path)
	return err == nil && strings.TrimSpace(out) == ""
}

// Staged lists files with staged changes, as absolute paths.
func (r Repo) Staged() ([]string, error) {
	return r.names("diff", "--cached", "--name-only", "-z", "--no-renames")
}

// Unstaged lists tracked files with unstaged changes, as absolute paths.
func (r Repo) Unstaged() ([]string, error) {
	return r.names("diff", "--name-only", "-z", "--no-renames")
}

// Untracked lists untracked, not ignored files, as absolute paths.
func (r Repo) Untracked() ([]string, error) {
	return r.names("ls-files", "--others", "--exclude-standard", "-z", "--full-name")
}

func (r Repo) names(args ...string) ([]string, error) {
	out, err := git(r.Root, args...)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, name := range strings.Split(out, "\x00") {
		if name != "" {
			paths = append(paths, filepath.Join(r.Root, name))
		}
	}
	return paths, nil
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = gitEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

// gitEnv keeps file-baton's git calls from taking the index lock or paging.
func gitEnv() []string {
	return append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_PAGER=cat", "LC_ALL=C")
}
