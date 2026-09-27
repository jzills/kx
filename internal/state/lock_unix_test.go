//go:build !windows

package state

import (
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// A filesystem that cannot flock (some NFS mounts answer ENOLCK) must not make
// every write fail: the save goes ahead unlocked, as it did before the lock.
func TestUnsupportedLockingDegradesToAnUnlockedWrite(t *testing.T) {
	original := tryLockFile
	tryLockFile = func(*os.File) (bool, error) { return false, unix.ENOLCK }
	t.Cleanup(func() { tryLockFile = original })

	service := newTestService(t, 10)
	if err := service.Save(State{Resources: pods("nginx"), Namespace: "prod"}); err != nil {
		t.Fatalf("Save on a filesystem without flock = %v, want it to write unlocked", err)
	}
	loaded, err := service.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if names := loaded.Names(); len(names) != 1 || names[0] != "nginx" {
		t.Errorf("Names() = %v, want [nginx] — the save did not write", names)
	}
}

// Only "cannot lock here" degrades. Any other failure to lock stays an error,
// since it says nothing about whether another kx is writing.
func TestOtherLockErrorsStayHard(t *testing.T) {
	original := tryLockFile
	tryLockFile = func(*os.File) (bool, error) { return false, unix.EACCES }
	t.Cleanup(func() { tryLockFile = original })

	service := newTestService(t, 10)
	if err := service.Save(State{Resources: pods("nginx"), Namespace: "prod"}); err == nil {
		t.Fatal("Save succeeded past a lock that failed with EACCES")
	}
	if _, err := os.Stat(service.Path); !os.IsNotExist(err) {
		t.Errorf("state file exists (%v) — the save wrote without the lock", err)
	}
}

// A state.json.lock the user can read but not write — left mode 0400, or
// root-owned 0644 by a `sudo kx` that kept HOME — must not brick every write.
// kx opens it read-only instead; flock locks a read-only descriptor just the
// same, so the write still excludes every other kx.
func TestReadOnlyLockFileStillLocks(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root opens a 0400 file for writing, so there is nothing to retry")
	}
	holder, writer := twoWriters(t, 10)
	if err := os.WriteFile(holder.Path+".lock", nil, 0o400); err != nil {
		t.Fatal(err)
	}

	save(t, writer, State{Resources: pods("nginx"), Namespace: "prod"})

	held, release := make(chan struct{}), make(chan struct{})
	holderDone := make(chan error, 1)
	go func() {
		holderDone <- holder.withLock(func() error {
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	writer.LockTimeout = 100 * time.Millisecond
	err := writer.Save(State{Resources: pods("redis"), Namespace: "prod"})
	close(release)
	if err == nil || !strings.Contains(err.Error(), "Another kx is updating") {
		t.Errorf("Save while a read-only lock is held = %v, want the lock-timeout refusal", err)
	}
	if err := <-holderDone; err != nil {
		t.Errorf("holder: %v", err)
	}
}

// Some flock implementations refuse an exclusive lock on a read-only
// descriptor (Linux's NFS emulation answers EBADF). Having opened the lock
// file read-only because it could not be written, kx then writes unlocked
// rather than failing, as it would had the open itself failed.
func TestReadOnlyLockFileThatCannotLockDegrades(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root opens a 0400 file for writing, so there is nothing to retry")
	}
	original := tryLockFile
	tryLockFile = func(*os.File) (bool, error) { return false, unix.EBADF }
	t.Cleanup(func() { tryLockFile = original })

	service := newTestService(t, 10)
	if err := os.WriteFile(service.Path+".lock", nil, 0o400); err != nil {
		t.Fatal(err)
	}
	if err := service.Save(State{Resources: pods("nginx"), Namespace: "prod"}); err != nil {
		t.Fatalf("Save through a read-only lock that cannot lock = %v, want it to write unlocked", err)
	}
}
