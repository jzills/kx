//go:build !windows

package state

import (
	"os"
	"testing"

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
