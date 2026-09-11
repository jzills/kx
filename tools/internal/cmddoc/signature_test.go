package cmddoc

import "testing"

// ShortSignature backs the README's command table, where the point is a column
// narrow enough to scan. A command whose flags leak back into it would widen
// every row of that table, so pick one that carries both an argument and a
// flag and assert the flag is gone.
func TestShortSignatureDropsFlags(t *testing.T) {
	cmd, ok := Commands()["delete"]
	if !ok {
		t.Fatal("delete is not registered")
	}
	if got, want := Signature(cmd), "kx delete <index>... [--yes/-y]"; got != want {
		t.Fatalf("Signature = %q, want %q", got, want)
	}
	if got, want := ShortSignature(cmd), "kx delete <index>..."; got != want {
		t.Fatalf("ShortSignature = %q, want %q", got, want)
	}
}
