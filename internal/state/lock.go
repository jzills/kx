package state

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// defaultLockTimeout is how long a writer waits for another kx to finish
// before giving up. A write holds the lock for one read and one rename, so
// anything near this long is a wedged process, not a busy one.
const defaultLockTimeout = 5 * time.Second

// lockPollInterval is how often a waiting writer retries. Polling a
// non-blocking lock rather than blocking on it is what makes the wait
// bounded: neither flock nor LockFileEx takes a timeout.
const lockPollInterval = 25 * time.Millisecond

// withLock runs fn while holding an exclusive lock on <path>.lock, so a
// load-modify-save inside it is atomic against every other writer of the same
// file — the CLI and a long-lived `kx mcp` server included.
//
// Without it, two writers that overlap both read the same file and the later
// rename throws the earlier write away: a `kx mark` made while an agent saves
// a listing simply vanishes.
//
// Each call opens its own descriptor. That is what makes the lock exclusive
// between goroutines of one process as well as between processes: flock and
// LockFileEx both belong to the open file (description/handle), not to the
// process, so two opens of one path contend.
//
// The lock file is never deleted. Unlinking it while another writer holds it
// would let a third writer create and lock a fresh inode beside it, and both
// would believe they were alone. A leftover file costs nothing: the OS drops
// the lock itself when its holder exits, however it exits.
//
// fn must not call withLock itself, on this or any Service for the same path:
// the inner call opens a second descriptor and waits on the outer one.
func (s *Service) withLock(fn func() error) error {
	path, err := s.path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()

	timeout := s.LockTimeout
	if timeout <= 0 {
		timeout = defaultLockTimeout
	}
	deadline := time.Now().Add(timeout)
	for {
		locked, err := tryLock(file)
		if err != nil {
			return fmt.Errorf("cannot lock %s: %w", file.Name(), err)
		}
		if locked {
			break
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("Another kx is updating %s — try again.", path)
		}
		time.Sleep(lockPollInterval)
	}
	// Deferred after Close, so it runs first. Closing would release the lock
	// anyway; unlocking first just says so.
	defer unlock(file)
	return fn()
}
