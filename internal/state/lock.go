package state

import (
	"errors"
	"fmt"
	"io/fs"
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

// tryLockFile is tryLock behind a seam, so a test can stand in a filesystem
// that does not support locking.
var tryLockFile = tryLock

// openLockFile is os.OpenFile behind a seam, so a test can stand in a lock
// file that cannot be opened at all.
var openLockFile = os.OpenFile

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
// A filesystem that cannot lock at all (flock over some NFS mounts, for one)
// runs fn unlocked rather than failing. Refusing would make every kx write
// fail for a user whose home lives there, over a race that needs two kx
// writers overlapping; running unlocked is exactly what kx did before the
// lock existed. Contention still waits, and any other error still fails.
//
// So does a lock file kx cannot open for writing — one a `sudo kx` left
// root-owned (macOS's sudo keeps HOME), say. Only an exclusive lock is ever
// taken, and flock and LockFileEx both take one on a read-only descriptor, so
// kx first retries the open read-only and locks that. If even that fails, fn
// runs unlocked: before the lock existed kx wrote state.json regardless of
// the lock file, and a leftover file nobody can open must not brick every
// write where it used to be harmless.
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
	file, readOnly, err := openLock(path + ".lock")
	if errors.Is(err, errUnopenableLock) {
		return fn()
	}
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
		locked, err := tryLockFile(file)
		if err != nil && (lockUnsupported(err) || readOnly) {
			// On a read-only descriptor, any failure to lock is one more way
			// of the lock file not being usable (Linux's flock emulation over
			// NFS refuses an exclusive lock on one with EBADF), so it
			// degrades the same way the open would have.
			return fn()
		}
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

// errUnopenableLock reports a lock file kx can open neither for writing nor
// for reading. withLock runs its fn unlocked on it.
var errUnopenableLock = errors.New("lock file cannot be opened")

// openLock opens the lock file at path for writing, creating it if absent.
// When that is refused for want of permission, it retries read-only and
// reports readOnly; when the retry fails too, it returns errUnopenableLock.
// Any other failure of the first open is returned as it is.
func openLock(path string) (file *os.File, readOnly bool, err error) {
	file, err = openLockFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err == nil {
		return file, false, nil
	}
	if !errors.Is(err, fs.ErrPermission) {
		return nil, false, err
	}
	file, err = openLockFile(path, os.O_RDONLY, 0)
	if err != nil {
		return nil, false, errUnopenableLock
	}
	return file, true, nil
}
