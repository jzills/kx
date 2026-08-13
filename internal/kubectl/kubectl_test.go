package kubectl

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

// contextShim installs a "kubectl" double that answers current-context from a
// file, so a test can change the answer the way `config use-context` would and
// count how often the double was actually consulted.
func contextShim(t *testing.T) (dir, contextFile, callsFile string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("PATH-shimmed test double assumes a POSIX shell")
	}
	dir = t.TempDir()
	contextFile = filepath.Join(dir, "context")
	callsFile = filepath.Join(dir, "calls")
	if err := os.WriteFile(contextFile, []byte("staging\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := "#!/bin/sh\n" +
		"if [ \"$1\" = config ] && [ \"$2\" = current-context ]; then\n" +
		"  echo current >> " + callsFile + "\n" +
		"  cat " + contextFile + "\n" +
		"elif [ \"$1\" = config ] && [ \"$2\" = use-context ]; then\n" +
		"  printf '%s\\n' \"$3\" > " + contextFile + "\n" +
		"fi\n"
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir, contextFile, callsFile
}

func contextCalls(t *testing.T, callsFile string) int {
	t.Helper()
	data, err := os.ReadFile(callsFile)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return len(strings.Fields(string(data)))
}

// Every index resolution checks the context — kx refuses an index counted in
// another cluster — and each check shelled out. `kx labels 1 2 3` spawned nine
// `kubectl config current-context` processes against three doing the work.
func TestCurrentContextIsReadOncePerProcess(t *testing.T) {
	_, _, calls := contextShim(t)
	client := New()

	for range 5 {
		if got := client.CurrentContext(); got != "staging" {
			t.Fatalf("CurrentContext() = %q, want staging", got)
		}
	}

	if got := contextCalls(t, calls); got != 1 {
		t.Errorf("spawned kubectl %d times for 5 reads, want 1", got)
	}
}

// kx changes the answer itself: `kx context 2` switches inside a single
// process, and a listing saved after that switch belongs to the new context.
// Caching without invalidating would stamp it with the one just left.
func TestSwitchingContextDropsTheCachedValue(t *testing.T) {
	_, _, calls := contextShim(t)
	client := New()

	if got := client.CurrentContext(); got != "staging" {
		t.Fatalf("CurrentContext() = %q, want staging", got)
	}
	if _, err := client.Run([]string{"config", "use-context", "production"}); err != nil {
		t.Fatalf("use-context: %v", err)
	}

	if got := client.CurrentContext(); got != "production" {
		t.Errorf("CurrentContext() = %q after switching, want production", got)
	}
	if got := contextCalls(t, calls); got != 2 {
		t.Errorf("spawned kubectl %d times, want 2 — one per side of the switch", got)
	}
}

// A kubeconfig with no current context is a legitimate setup, and "none set"
// is as worth remembering as a name — re-asking spawned a subprocess to be
// told the same thing.
func TestUnsetContextIsCachedToo(t *testing.T) {
	_, contextFile, calls := contextShim(t)
	if err := os.WriteFile(contextFile, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	client := New()

	for range 3 {
		if got := client.CurrentContext(); got != "" {
			t.Fatalf("CurrentContext() = %q, want empty", got)
		}
	}
	if got := contextCalls(t, calls); got != 1 {
		t.Errorf("spawned kubectl %d times for 3 reads, want 1", got)
	}
}
