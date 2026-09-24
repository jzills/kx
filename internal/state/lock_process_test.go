package state

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

// The lock exists to serialise separate processes — the CLI and a `kx mcp`
// server — and the in-process tests only stand in for that. This one runs
// the real shape: several copies of the test binary, re-executed as helpers,
// each writing its own marks to one state file. Every mark must survive.
const (
	lockHelperEnv    = "KX_STATE_LOCK_HELPER"
	lockHelperPath   = "KX_STATE_LOCK_HELPER_PATH"
	lockHelperWriter = "KX_STATE_LOCK_HELPER_WRITER"
	helperWrites     = 15
)

func TestConcurrentProcessesKeepEveryMark(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns helper processes")
	}
	const processes = 4
	path := filepath.Join(t.TempDir(), "state.json")

	commands := make([]*exec.Cmd, processes)
	for i := range commands {
		cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$")
		cmd.Env = append(os.Environ(),
			lockHelperEnv+"=1", lockHelperPath+"="+path, lockHelperWriter+"="+strconv.Itoa(i))
		if err := cmd.Start(); err != nil {
			t.Fatalf("starting helper %d: %v", i, err)
		}
		commands[i] = cmd
	}
	for i, cmd := range commands {
		if err := cmd.Wait(); err != nil {
			t.Errorf("helper %d: %v", i, err)
		}
	}

	marks, err := (&Service{MaxHistory: 10, Path: path}).Marks()
	if err != nil {
		t.Fatalf("Marks: %v", err)
	}
	for i := range processes {
		for j := range helperWrites {
			if _, ok := marks[helperMarkName(i, j)]; !ok {
				t.Errorf("mark %s was lost", helperMarkName(i, j))
			}
		}
	}
}

func helperMarkName(writer, write int) string { return fmt.Sprintf("w%d-%d", writer, write) }

// TestHelperProcess is not a test: it is the body of one helper process for
// TestConcurrentProcessesKeepEveryMark, and does nothing outside one.
func TestHelperProcess(t *testing.T) {
	if os.Getenv(lockHelperEnv) != "1" {
		t.Skip("only runs as a helper process")
	}
	writer, err := strconv.Atoi(os.Getenv(lockHelperWriter))
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{MaxHistory: 10, Path: os.Getenv(lockHelperPath)}
	for j := range helperWrites {
		name := helperMarkName(writer, j)
		if err := service.SaveMark(name, podMark(name)); err != nil {
			t.Fatalf("SaveMark %s: %v", name, err)
		}
	}
}
