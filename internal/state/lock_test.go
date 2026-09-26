package state

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jzills/kx/internal/kinds"
)

// twoWriters is two services on one file, the in-process stand-in for the CLI
// and a `kx mcp` server. Each lock attempt opens its own descriptor, and a
// flock belongs to the open file description, so these contend exactly the
// way two processes would.
func twoWriters(t *testing.T, maxHistory int) (*Service, *Service) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.json")
	return &Service{MaxHistory: maxHistory, Path: path}, &Service{MaxHistory: maxHistory, Path: path}
}

func podMark(name string) Mark {
	return Mark{Resource: Resource{Name: name, Kind: kinds.Pod, Namespace: "prod"}}
}

// A load-modify-save that is not atomic loses a write whenever two of them
// overlap: both read the same file, each adds its own mark, and the later
// rename throws the earlier one away.
func TestConcurrentMarksAreAllKept(t *testing.T) {
	cli, server := twoWriters(t, 10)
	const writers = 20
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := range writers {
		writer := cli
		if i%2 == 1 {
			writer = server
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- writer.SaveMark(fmt.Sprintf("m%d", i), podMark(fmt.Sprintf("pod-%d", i)))
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("SaveMark: %v", err)
		}
	}
	marks, err := cli.Marks()
	if err != nil {
		t.Fatalf("Marks: %v", err)
	}
	for i := range writers {
		if _, ok := marks[fmt.Sprintf("m%d", i)]; !ok {
			t.Errorf("mark m%d was lost", i)
		}
	}
}

// The realistic shape of the race: an agent saving listings while the user
// marks things in a terminal. Neither side may drop the other's write.
func TestConcurrentSavesAndMarksInterleave(t *testing.T) {
	const listings, maxHistory = 10, 20
	cli, server := twoWriters(t, maxHistory)
	var wg sync.WaitGroup
	errs := make(chan error, 2*listings)
	for i := range listings {
		wg.Add(2)
		go func() {
			defer wg.Done()
			errs <- server.Save(State{Resources: pods(fmt.Sprintf("listing-%d", i)), Namespace: "prod"})
		}()
		go func() {
			defer wg.Done()
			errs <- cli.SaveMark(fmt.Sprintf("m%d", i), podMark(fmt.Sprintf("pod-%d", i)))
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("write: %v", err)
		}
	}
	history, err := cli.LoadHistory()
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	for i := range listings {
		if _, ok := history.Marks[fmt.Sprintf("m%d", i)]; !ok {
			t.Errorf("mark m%d was lost", i)
		}
	}
	if want := min(listings, maxHistory); len(history.States) != want {
		t.Errorf("stack holds %d entries, want %d — a listing was lost", len(history.States), want)
	}
}

// A holder that crashed leaves its .lock file behind; the OS has already
// released the lock with the process, so the file alone must not block.
func TestStaleLockFileDoesNotBlock(t *testing.T) {
	service := newTestService(t, 10)
	lockPath := service.Path + ".lock"
	if err := os.WriteFile(lockPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	service.LockTimeout = 100 * time.Millisecond
	save(t, service, State{Resources: pods("nginx"), Namespace: "prod"})
	if _, err := os.Stat(lockPath); err != nil {
		t.Errorf("lock file gone after a save (%v) — deleting it while held breaks exclusion", err)
	}
}

// A writer that cannot get the lock gives up after LockTimeout with an error
// the user can act on, and leaves the file as it found it.
func TestLockTimeoutRefusesTheWrite(t *testing.T) {
	holder, writer := twoWriters(t, 10)
	save(t, holder, State{Resources: pods("nginx"), Namespace: "prod"})
	before, err := os.ReadFile(holder.Path)
	if err != nil {
		t.Fatal(err)
	}

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
	released := false
	releaseHolder := func() {
		if !released {
			released = true
			close(release)
		}
	}
	t.Cleanup(releaseHolder)

	writer.LockTimeout = 100 * time.Millisecond
	result := make(chan error, 1)
	go func() {
		result <- writer.Save(State{Resources: pods("redis"), Namespace: "prod"})
	}()
	select {
	case err = <-result:
	case <-time.After(time.Second):
		// Let the writer finish before failing, so it cannot outlive the test
		// and race its cleanup.
		releaseHolder()
		<-result
		t.Fatal("Save waited past its 100ms LockTimeout — the deadline is ignored")
	}
	if err == nil || !strings.Contains(err.Error(), "Another kx is updating "+writer.Path+" — try again.") {
		t.Fatalf("Save = %v, want the lock-timeout refusal", err)
	}
	after, err := os.ReadFile(holder.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("a Save that timed out on the lock still changed the file")
	}

	releaseHolder()
	if err := <-holderDone; err != nil {
		t.Errorf("holder: %v", err)
	}
}

// A zero LockTimeout is the default wait, not "give up at once": a Service
// built literally, as tests and callers do, must still wait for a holder.
func TestZeroLockTimeoutWaitsForTheHolder(t *testing.T) {
	holder, writer := twoWriters(t, 10)
	held := make(chan struct{})
	holderDone := make(chan error, 1)
	go func() {
		holderDone <- holder.withLock(func() error {
			close(held)
			time.Sleep(200 * time.Millisecond)
			return nil
		})
	}()
	<-held
	if err := writer.SaveMark("m", podMark("pod")); err != nil {
		t.Errorf("SaveMark with a zero LockTimeout = %v, want it to wait out a 200ms hold", err)
	}
	if err := <-holderDone; err != nil {
		t.Errorf("holder: %v", err)
	}
}

func TestSaveMarkIfAbsentReturnsTheExistingMark(t *testing.T) {
	service := newTestService(t, 10)
	original := podMark("api-7d8f")
	if err := service.SaveMark("api", original); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(service.Path)
	if err != nil {
		t.Fatal(err)
	}
	existing, err := service.SaveMarkIfAbsent("api", podMark("other"))
	if err != nil {
		t.Fatalf("SaveMarkIfAbsent: %v", err)
	}
	if existing == nil || *existing != original {
		t.Errorf("existing = %+v, want %+v", existing, original)
	}
	after, err := os.ReadFile(service.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("SaveMarkIfAbsent wrote over a taken name")
	}
}

func TestSaveMarkIfAbsentWritesAFreeName(t *testing.T) {
	service := newTestService(t, 10)
	existing, err := service.SaveMarkIfAbsent("api", podMark("api-7d8f"))
	if err != nil || existing != nil {
		t.Fatalf("SaveMarkIfAbsent = %+v, %v; want nil, nil", existing, err)
	}
	marks, err := service.Marks()
	if err != nil {
		t.Fatal(err)
	}
	if marks["api"] != podMark("api-7d8f") {
		t.Errorf("marks = %+v, want api on api-7d8f", marks)
	}
}

// The check and the write share one lock hold, so of many writers racing
// for one name exactly one gets it and every other is told who did.
func TestSaveMarkIfAbsentConcurrentlyWritesOnce(t *testing.T) {
	cli, server := twoWriters(t, 10)
	const writers = 10
	var wg sync.WaitGroup
	var mu sync.Mutex
	wrote := []string{}
	for i := range writers {
		writer := cli
		if i%2 == 1 {
			writer = server
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			pod := fmt.Sprintf("pod-%d", i)
			existing, err := writer.SaveMarkIfAbsent("same", podMark(pod))
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err != nil:
				t.Errorf("SaveMarkIfAbsent: %v", err)
			case existing == nil:
				wrote = append(wrote, pod)
			}
		}()
	}
	wg.Wait()
	if len(wrote) != 1 {
		t.Fatalf("%d writers took the name (%v), want exactly 1", len(wrote), wrote)
	}
	marks, err := cli.Marks()
	if err != nil {
		t.Fatal(err)
	}
	if marks["same"].Name != wrote[0] {
		t.Errorf("@same is on %s, but the writer told it won was %s", marks["same"].Name, wrote[0])
	}
}

// A reader that meets a file of another schema version resets it, and that
// write takes the lock like any other: it must not replace a file another kx
// is in the middle of rewriting.
func TestSchemaResetByAReaderTakesTheLock(t *testing.T) {
	holder, reader := twoWriters(t, 10)
	old := []byte(`{"version": 0, "states": [], "cursor": 0}`)
	if err := os.WriteFile(holder.Path, old, 0o600); err != nil {
		t.Fatal(err)
	}
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

	reader.LockTimeout = 100 * time.Millisecond
	result := make(chan error, 1)
	go func() {
		_, err := reader.Load()
		result <- err
	}()
	var err error
	select {
	case err = <-result:
	case <-time.After(time.Second):
		close(release)
		<-result
		t.Fatal("Load waited past its 100ms LockTimeout — the deadline is ignored")
	}
	close(release)
	if err == nil || !strings.Contains(err.Error(), "Another kx is updating") {
		t.Errorf("Load = %v, want the lock-timeout refusal", err)
	}
	if after, _ := os.ReadFile(holder.Path); !bytes.Equal(after, old) {
		t.Error("a reader reset the file while another kx held the lock")
	}
	if err := <-holderDone; err != nil {
		t.Errorf("holder: %v", err)
	}
}

// A reader that saw an old-version file waits for the lock to reset it, and by
// the time it has the lock another kx may have rewritten the file at the
// current version. The reader must read that file, not reset over it.
func TestSchemaResetByAReaderRereadsUnderTheLock(t *testing.T) {
	holder, reader := twoWriters(t, 10)
	if err := os.WriteFile(holder.Path, []byte(`{"version": 0, "states": [], "cursor": 0}`), 0o600); err != nil {
		t.Fatal(err)
	}
	held, release := make(chan struct{}), make(chan struct{})
	holderDone := make(chan error, 1)
	go func() {
		holderDone <- holder.withLock(func() error {
			close(held)
			<-release
			// The newer write, landing while the reader waits on the lock.
			return holder.saveHistory(History{
				States: []State{{Resources: pods("nginx"), Namespace: "prod"}},
				Marks:  map[string]Mark{"keep": podMark("nginx")},
			})
		})
	}()
	<-held

	// Learn when the reader has read the old file and gone for the lock.
	original := tryLockFile
	waiting := make(chan struct{})
	var once sync.Once
	tryLockFile = func(file *os.File) (bool, error) {
		once.Do(func() { close(waiting) })
		return original(file)
	}
	type loaded struct {
		state State
		err   error
	}
	result := make(chan loaded, 1)
	go func() {
		state, err := reader.Load()
		result <- loaded{state, err}
	}()
	<-waiting
	close(release)
	got := <-result
	tryLockFile = original
	if err := <-holderDone; err != nil {
		t.Fatalf("holder: %v", err)
	}

	if got.err != nil {
		t.Fatalf("Load = %v, want the listing written while it waited", got.err)
	}
	if names := got.state.Names(); len(names) != 1 || names[0] != "nginx" {
		t.Errorf("Load read %v, want [nginx]", names)
	}
	marks, err := reader.Marks()
	if err != nil || marks["keep"] != podMark("nginx") {
		t.Errorf("marks = %+v (%v) — the reader reset over the newer file", marks, err)
	}
}

// A lock file kx cannot open at all — neither for writing nor, on retry, for
// reading — must not make every write fail. Before the lock existed kx wrote
// state.json regardless of the lock file; it still does, unlocked, the way it
// does on a filesystem that cannot lock.
func TestUnopenableLockFileDegradesToAnUnlockedWrite(t *testing.T) {
	original := openLockFile
	var flags []int
	openLockFile = func(name string, flag int, perm os.FileMode) (*os.File, error) {
		flags = append(flags, flag)
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
	}
	t.Cleanup(func() { openLockFile = original })

	service := newTestService(t, 10)
	if err := service.Save(State{Resources: pods("nginx"), Namespace: "prod"}); err != nil {
		t.Fatalf("Save past an unopenable lock file = %v, want it to write unlocked", err)
	}
	if len(flags) != 2 || flags[1]&(os.O_WRONLY|os.O_RDWR) != 0 {
		t.Errorf("open flags = %v, want a read-write open and then a read-only retry", flags)
	}
	loaded, err := service.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if names := loaded.Names(); len(names) != 1 || names[0] != "nginx" {
		t.Errorf("Names() = %v, want [nginx] — the save did not write", names)
	}
}

// Only a permission failure degrades. Any other failure to open the lock file
// stays an error — it says nothing about whether the user merely lacks write
// access to a file some other kx left behind.
func TestOtherLockFileOpenErrorsStayHard(t *testing.T) {
	original := openLockFile
	openLockFile = func(name string, flag int, perm os.FileMode) (*os.File, error) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: errors.New("input/output error")}
	}
	t.Cleanup(func() { openLockFile = original })

	service := newTestService(t, 10)
	if err := service.Save(State{Resources: pods("nginx"), Namespace: "prod"}); err == nil {
		t.Fatal("Save succeeded past a lock file that failed to open with an I/O error")
	}
	if _, err := os.Stat(service.Path); !os.IsNotExist(err) {
		t.Errorf("state file exists (%v) — the save wrote without the lock", err)
	}
}
