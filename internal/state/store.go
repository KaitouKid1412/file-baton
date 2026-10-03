package state

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// LockTimeout is how long Update waits for another process to finish.
var LockTimeout = 3 * time.Second

// ErrBusy means the state lock could not be taken within LockTimeout.
var ErrBusy = errors.New("file-baton state is busy")

// Store is the on-disk home of one repository's state.
type Store struct {
	Dir string // <git-common-dir>/file-baton
}

// Open returns the store in dir, creating the directory if needed.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "snapshots"), 0o700); err != nil {
		return nil, err
	}
	return &Store{Dir: dir}, nil
}

func (s *Store) statePath() string { return filepath.Join(s.Dir, "state.json") }

// Update runs fn on the current state under the exclusive lock and saves the
// result. When fn returns an error nothing is saved.
func (s *Store) Update(fn func(*State) error) error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	st, err := s.load()
	if err != nil {
		return err
	}
	if err := fn(st); err != nil {
		return err
	}
	return s.save(st)
}

func (s *Store) lock() (func(), error) {
	f, err := os.OpenFile(filepath.Join(s.Dir, "state.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(LockTimeout)
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				_ = f.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) || time.Now().After(deadline) {
			_ = f.Close()
			if errors.Is(err, syscall.EWOULDBLOCK) {
				return nil, ErrBusy
			}
			return nil, err
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (s *Store) load() (*State, error) {
	data, err := os.ReadFile(s.statePath())
	if errors.Is(err, os.ErrNotExist) {
		return New(), nil
	}
	if err != nil {
		return nil, err
	}
	st := New()
	if err := json.Unmarshal(data, st); err != nil || st.Version != SchemaVersion {
		// A corrupt or foreign document must never wedge every session: start over.
		return New(), nil
	}
	st.normalize()
	return st, nil
}

func (s *Store) save(st *State) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.Dir, "state-*.json")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), s.statePath())
}

// SnapshotPath is where the snapshot called name is kept.
func (s *Store) SnapshotPath(name string) string {
	return filepath.Join(s.Dir, "snapshots", name)
}

// WriteSnapshot copies the file at path aside for a later diff and returns the
// snapshot's name, or "" when the file does not exist yet.
func (s *Store) WriteSnapshot(path, sid string) (string, error) {
	src, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer src.Close()
	sum := sha1.Sum([]byte(path))
	name := fmt.Sprintf("%s-%s", hex.EncodeToString(sum[:8]), short(sid))
	dst, err := os.OpenFile(s.SnapshotPath(name), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return "", err
	}
	return name, dst.Close()
}

// RemoveSnapshot deletes a snapshot; an empty name is a no-op.
func (s *Store) RemoveSnapshot(name string) {
	if name != "" {
		_ = os.Remove(s.SnapshotPath(name))
	}
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
