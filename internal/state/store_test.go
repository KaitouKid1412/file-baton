package state

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestConcurrentUpdatesAreSerialized(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const writers = 40
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Each writer opens the lock file on its own, as separate processes do.
			if err := s.Update(func(st *State) error {
				st.Sessions[fmt.Sprint(i)] = &Session{ID: fmt.Sprint(i)}
				return nil
			}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	var n int
	_ = s.Update(func(st *State) error { n = len(st.Sessions); return nil })
	if n != writers {
		t.Fatalf("lost updates: %d sessions, want %d", n, writers)
	}
}

func TestFailedUpdateSavesNothing(t *testing.T) {
	s, _ := Open(t.TempDir())
	_ = s.Update(func(st *State) error {
		st.Sessions["a"] = &Session{ID: "a"}
		return fmt.Errorf("boom")
	})
	_ = s.Update(func(st *State) error {
		if len(st.Sessions) != 0 {
			t.Fatal("failed update was saved")
		}
		return nil
	})
}

func TestCorruptStateStartsOver(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Update(func(st *State) error {
		if st.Sessions == nil || st.Locks == nil || st.Touched == nil {
			t.Fatal("maps not initialized")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshots(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	if name, err := s.WriteSnapshot(filepath.Join(dir, "missing"), "sid"); err != nil || name != "" {
		t.Fatalf("missing file: %q %v", name, err)
	}
	f := filepath.Join(dir, "f.txt")
	_ = os.WriteFile(f, []byte("hello"), 0o600)
	name, err := s.WriteSnapshot(f, "0123456789")
	if err != nil || name == "" {
		t.Fatalf("snapshot: %q %v", name, err)
	}
	if data, _ := os.ReadFile(s.SnapshotPath(name)); string(data) != "hello" {
		t.Fatalf("snapshot content = %q", data)
	}
	s.RemoveSnapshot(name)
	if _, err := os.Stat(s.SnapshotPath(name)); !os.IsNotExist(err) {
		t.Fatal("snapshot not removed")
	}
}
