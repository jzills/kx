package cli

import (
	"runtime"
	"testing"
	"time"
	"weak"
)

// A root and its commands are garbage once dropped. recorders and versionTexts
// were process-wide maps keyed by command, so every root a test built — about
// a megabyte of command tree each — stayed reachable for the whole run.
func TestADroppedRootIsCollected(t *testing.T) {
	root := NewRoot(Services{}, "test")
	// delete registers kubectl's flags, so it was a recorders key.
	child, _, err := root.Find([]string{"delete"})
	if err != nil || child == root {
		t.Fatalf("root.Find(delete) = %v, %v", child, err)
	}
	if recordedKubectlArgs(child) == nil {
		t.Fatal("kx delete has no recorder — the lookup found nothing to retain")
	}
	rootRef, childRef := weak.Make(root), weak.Make(child)
	if _, ok := versionTexts.Load(rootRef); !ok {
		t.Fatal("the root has no --version entry — nothing to see dropped")
	}
	root, child = nil, nil
	for range 3 {
		runtime.GC()
	}
	if rootRef.Value() != nil {
		t.Error("the root is still reachable after being dropped")
	}
	if childRef.Value() != nil {
		t.Error("kx delete is still reachable after its root was dropped")
	}
	// The root's --version entry goes with it, rather than leaving a dead key
	// behind for every root built. A cleanup runs after collection, not
	// during it, so it is waited for.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, ok := versionTexts.Load(rootRef); !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the dropped root's --version entry is still in versionTexts")
		}
		runtime.GC()
		time.Sleep(10 * time.Millisecond)
	}
}
