//go:build !unix

package kubectl

// processAlive is never reached off Unix: the only test calling it installs
// its kubectl double with shim, which skips there. It exists so the package's
// tests still compile on Windows.
func processAlive(int) bool {
	panic("processAlive is Unix-only")
}
