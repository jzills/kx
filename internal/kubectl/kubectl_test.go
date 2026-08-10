package kubectl

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
)

// onLine returning an error must not leave the subprocess running behind
// it. Watch backs `kubectl get --watch`, which never exits on its own, so
// anything short of killing the process here means the Wait() that follows
// blocks forever on a process nothing is draining stdout from anymore —
// the exact hang this is a regression test for.
//
// Only kubectl.Service (the interface) is fakeable from internal/cli;
// exercising this requires Exec itself, which means a real subprocess.
// PATH is shimmed with a "kubectl" double rather than talking to a cluster,
// keeping this hermetic.
func TestWatchKillsSubprocessOnOnLineError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH-shimmed test double assumes a POSIX shell")
	}

	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	script := filepath.Join(dir, "kubectl")
	// Stands in for `kubectl get --watch` against an idle cluster: records
	// its own PID, prints the one line Watch will read, then — like the
	// real thing — never exits on its own. `exec sleep` replaces the shell
	// with sleep in place (same PID) rather than forking a child of it;
	// kubectl is a single process with no shell in front of it, and a
	// forked-child sleep would let Kill() reap the shell while the sleep
	// it orphaned keeps the stderr pipe open underneath it, hanging this
	// test on an artifact of the double rather than the thing under test.
	body := "#!/bin/sh\necho \"$$\" > " + pidFile + "\necho header\nexec sleep 300\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	done := make(chan error, 1)
	go func() {
		done <- Exec{}.Watch(nil, func(line string) error {
			return errors.New("stop")
		})
	}()

	select {
	case err := <-done:
		if err == nil || err.Error() != "stop" {
			t.Fatalf("Watch() = %v, want the onLine error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Watch did not return — the subprocess was likely left running")
	}

	// The script writes its PID before ever printing the header line Watch
	// reads, so it's already on disk by the time Watch (and onLine) can
	// have run at all.
	pidBytes, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("kubectl double never started: %v", err)
	}
	var pid int
	if _, err := fmt.Sscanf(string(pidBytes), "%d", &pid); err != nil {
		t.Fatalf("unreadable pid file %q: %v", pidBytes, err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		if err := syscall.Kill(pid, 0); err != nil {
			return // gone, as expected
		}
		if time.Now().After(deadline) {
			t.Fatalf("kubectl subprocess (pid %d) still running after Watch returned", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
