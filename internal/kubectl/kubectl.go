// Package kubectl wraps the kubectl binary. kx delegates all resource
// operations to kubectl rather than the API server so that arbitrary flags pass
// through untouched and kubectl keeps ownership of kubeconfig resolution, auth
// and credential exec plugins.
package kubectl

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"strings"
)

const missingKubectl = "kubectl not found on PATH — install kubectl " +
	"(https://kubernetes.io/docs/tasks/tools/) and ensure it is on your PATH."

// Service is the interface commands depend on, so tests can substitute a fake
// without spawning processes.
type Service interface {
	// Run captures stdout and fails on a non-zero exit.
	Run(args []string) (string, error)
	// RunInteractive streams stdio through to the terminal and returns the
	// exit code.
	RunInteractive(args []string, quietStderr bool) (int, error)
	// Watch streams stdout line by line to onLine as kubectl produces it, for
	// long-running commands (get --watch) where Run's full-buffer capture
	// would block forever — kubectl get --watch never exits on its own.
	// Stderr is captured and surfaced as the error on a non-zero exit,
	// matching Run. An onLine error stops the stream early, kills the
	// subprocess (since it would otherwise keep running with nothing left
	// to drain its output), and is returned.
	Watch(args []string, onLine func(line string) error) error
	// Probe runs silently and returns only the exit code.
	Probe(args []string) int
	CurrentNamespace() string
	CurrentContext() string
}

// Exec is the real Service, shelling out to kubectl.
type Exec struct{}

// New returns a kubectl service backed by the kubectl binary.
func New() *Exec { return &Exec{} }

// command builds the exec.Cmd. A missing kubectl surfaces at Start/Run time as
// exec.ErrNotFound; every entry point below translates it into an actionable
// error rather than letting a bare "executable file not found" escape.
func (Exec) command(args []string) *exec.Cmd {
	return exec.Command("kubectl", args...)
}

func translate(err error) error {
	if errors.Is(err, exec.ErrNotFound) {
		return errors.New(missingKubectl)
	}
	return err
}

// Run captures stdout, returning the trimmed stderr as the error on a non-zero
// exit so the caller can render kubectl's own message.
func (e Exec) Run(args []string) (string, error) {
	cmd := e.command(args)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return "", errors.New(strings.TrimSpace(stderr.String()))
	}
	if err != nil {
		return "", translate(err)
	}
	return stdout.String(), nil
}

// RunInteractive wires stdio straight through, which is what `exec`, `edit` and
// `port-forward` need to keep their TTY behavior.
func (e Exec) RunInteractive(args []string, quietStderr bool) (int, error) {
	cmd := e.command(args)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	if !quietStderr {
		cmd.Stderr = os.Stderr
	}

	err := cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	if err != nil {
		return 1, translate(err)
	}
	return 0, nil
}

// Watch streams stdout line by line, for commands that never exit on their
// own (kubectl get --watch). Buffering the whole output the way Run does
// would block forever, since there is no "process exits" moment to wait for.
func (e Exec) Watch(args []string, onLine func(line string) error) error {
	cmd := e.command(args)
	var stderr strings.Builder
	cmd.Stderr = &stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return translate(err)
	}
	if err := cmd.Start(); err != nil {
		return translate(err)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var onLineErr error
	for scanner.Scan() {
		if err := onLine(scanner.Text()); err != nil {
			onLineErr = err
			break
		}
	}

	if onLineErr != nil {
		// onLine stopped the stream early — e.g. runWatch bailing out on a
		// header line it can't make sense of. kubectl get --watch never exits
		// on its own, so with nothing draining stdout anymore, a plain
		// cmd.Wait() here would block forever on a process that is still
		// running. Kill it first; the Wait() that follows only reaps it.
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return onLineErr
	}

	waitErr := cmd.Wait()
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		return errors.New(strings.TrimSpace(stderr.String()))
	}
	if waitErr != nil {
		return translate(waitErr)
	}
	return nil
}

// Probe runs silently and reports only the exit code, used for shell detection.
func (e Exec) Probe(args []string) int {
	cmd := e.command(args)
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		return 1
	}
	return 0
}

// CurrentNamespace reports the active namespace, falling back to "default".
//
// Best-effort: no kubeconfig or no current context exits non-zero. The
// namespace is only a label here, so a failure must not become a command error.
func (e Exec) CurrentNamespace() string {
	out, err := e.Run([]string{"config", "view", "--minify", "-o", "jsonpath={..namespace}"})
	if err != nil {
		return "default"
	}
	if ns := strings.TrimSpace(out); ns != "" {
		return ns
	}
	return "default"
}

// CurrentContext reports the active context, or "" when none is set, so context
// listing still works instead of failing.
func (e Exec) CurrentContext() string {
	out, err := e.Run([]string{"config", "current-context"})
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

var _ Service = (*Exec)(nil)
