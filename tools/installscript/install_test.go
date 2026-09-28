package installscript

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

const scriptPath = "../../site/static/install.sh"

// release is a fake GitHub release server. It answers the two URL shapes the
// script uses — latest/download/<asset> and download/<tag>/<asset> — for every
// platform, with a SHA256SUMS listing each archive the way the real release
// workflow writes it.
type release struct {
	*httptest.Server
	mu       sync.Mutex
	requests []string
}

type releaseOptions struct {
	tamper   bool // serve archives whose bytes differ from the listed hash
	unlisted bool // omit archive lines from SHA256SUMS
}

var platforms = []string{"linux_amd64", "linux_arm64", "darwin_amd64", "darwin_arm64"}

func newRelease(t *testing.T, version string, opts releaseOptions) *release {
	t.Helper()
	files := map[string][]byte{}
	var sums strings.Builder
	for _, p := range platforms {
		archive := stubArchive(t, version)
		sum := sha256.Sum256(archive)
		for _, name := range []string{"kx_" + p + ".tar.gz", "kx_" + version + "_" + p + ".tar.gz"} {
			if !opts.unlisted {
				fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(sum[:]), name)
			}
			served := archive
			if opts.tamper {
				served = append(append([]byte{}, archive...), 0)
			}
			files[name] = served
		}
	}
	files["SHA256SUMS"] = []byte(sums.String())

	r := &release{}
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.requests = append(r.requests, req.URL.Path)
		r.mu.Unlock()
		name := ""
		switch {
		case strings.HasPrefix(req.URL.Path, "/latest/download/"):
			name = strings.TrimPrefix(req.URL.Path, "/latest/download/")
		case strings.HasPrefix(req.URL.Path, "/download/"+version+"/"):
			name = strings.TrimPrefix(req.URL.Path, "/download/"+version+"/")
		}
		body, ok := files[name]
		if !ok {
			http.NotFound(w, req)
			return
		}
		w.Write(body)
	}))
	t.Cleanup(r.Close)
	return r
}

func (r *release) paths() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.requests...)
}

// stubArchive builds kx/kx (a shell script printing its version) and
// kx/LICENSE, gzipped, in the layout scripts/build_binaries.sh produces.
func stubArchive(t *testing.T, version string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	add := func(name string, mode int64, body string) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	add("kx/kx", 0o755, "#!/bin/sh\necho 'kx "+version+"'\n")
	add("kx/LICENSE", 0o644, "MIT\n")
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// declaredTools is every external command the script may call. A run's PATH
// holds only these, so a script that quietly starts depending on another tool
// fails here rather than on a machine without it. gzip is on the list because
// GNU tar execs it for -z.
var declaredTools = []string{"curl", "wget", "tar", "gzip", "uname", "mktemp", "mkdir", "cp", "chmod", "mv", "rm",
	"sha256sum", "shasum", "sysctl"}

// toolbox returns a directory of symlinks to the declared tools that exist on
// this machine, minus any named in omit.
func toolbox(t *testing.T, omit ...string) string {
	t.Helper()
	dir := t.TempDir()
	skip := map[string]bool{}
	for _, name := range omit {
		skip[name] = true
	}
	for _, name := range declaredTools {
		if skip[name] {
			continue
		}
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		if err := os.Symlink(path, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// fakeCommand writes an executable sh script called name into dir.
func fakeCommand(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

type result struct {
	stdout, stderr string
	code           int
	home           string
}

// runScript runs script under /bin/sh with a clean environment: HOME is a
// fresh temp dir, PATH is exactly pathDirs, and env adds to both.
func runScript(t *testing.T, script string, r *release, env map[string]string, pathDirs ...string) result {
	t.Helper()
	home := t.TempDir()
	cmd := exec.Command("/bin/sh", script)
	cmd.Env = []string{
		"HOME=" + home,
		"PATH=" + strings.Join(pathDirs, string(os.PathListSeparator)),
		"KX_INSTALL_BASE_URL=" + r.URL,
	}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+strings.ReplaceAll(v, "$HOME", home))
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("running %s: %v", script, err)
	}
	return result{stdout: stdout.String(), stderr: stderr.String(), code: code, home: home}
}

// hostPlatform is the kx_<os>_<arch> suffix the script should pick on this
// machine when uname is not faked.
func hostPlatform(t *testing.T) string {
	t.Helper()
	switch runtime.GOOS + "_" + runtime.GOARCH {
	case "linux_amd64", "linux_arm64", "darwin_amd64", "darwin_arm64":
		return runtime.GOOS + "_" + runtime.GOARCH
	}
	t.Skipf("no kx build for %s/%s", runtime.GOOS, runtime.GOARCH)
	return ""
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func mustNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		t.Errorf("%s exists, want nothing installed", path)
	}
}

func TestInstallsNewestIntoLocalBin(t *testing.T) {
	p := hostPlatform(t)
	r := newRelease(t, "v0.7.0", releaseOptions{})
	res := runScript(t, scriptPath, r, nil, toolbox(t))
	if res.code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", res.code, res.stdout, res.stderr)
	}
	bin := filepath.Join(res.home, ".local", "bin", "kx")
	out, err := exec.Command(bin, "--version").Output()
	if err != nil || strings.TrimSpace(string(out)) != "kx v0.7.0" {
		t.Fatalf("installed %s --version = %q, %v", bin, out, err)
	}
	if want := "✓ Installed kx v0.7.0 to " + bin; !strings.Contains(res.stdout, want) {
		t.Errorf("stdout = %q, want %q", res.stdout, want)
	}
	for _, want := range []string{"/latest/download/kx_" + p + ".tar.gz", "/latest/download/SHA256SUMS"} {
		if !contains(r.paths(), want) {
			t.Errorf("requests = %v, want %s", r.paths(), want)
		}
	}
}

func TestPinnedVersion(t *testing.T) {
	p := hostPlatform(t)
	for _, spelled := range []string{"v0.7.0", "0.7.0"} {
		t.Run(spelled, func(t *testing.T) {
			r := newRelease(t, "v0.7.0", releaseOptions{})
			res := runScript(t, scriptPath, r, map[string]string{"KX_VERSION": spelled}, toolbox(t))
			if res.code != 0 {
				t.Fatalf("exit %d, stderr: %s", res.code, res.stderr)
			}
			for _, want := range []string{"/download/v0.7.0/kx_v0.7.0_" + p + ".tar.gz", "/download/v0.7.0/SHA256SUMS"} {
				if !contains(r.paths(), want) {
					t.Errorf("requests = %v, want %s", r.paths(), want)
				}
			}
			for _, got := range r.paths() {
				if strings.HasPrefix(got, "/latest/") {
					t.Errorf("pinned run requested %s", got)
				}
			}
		})
	}
}

func TestUnknownVersionNamesTheURL(t *testing.T) {
	hostPlatform(t)
	r := newRelease(t, "v0.7.0", releaseOptions{})
	res := runScript(t, scriptPath, r, map[string]string{"KX_VERSION": "v9.9.9"}, toolbox(t))
	if res.code == 0 || !strings.Contains(res.stderr, r.URL+"/download/v9.9.9/") {
		t.Fatalf("exit %d, stderr %q: want a failure naming the URL", res.code, res.stderr)
	}
	mustNotExist(t, filepath.Join(res.home, ".local", "bin", "kx"))
}

func TestHonoursInstallDir(t *testing.T) {
	hostPlatform(t)
	r := newRelease(t, "v0.7.0", releaseOptions{})
	dir := filepath.Join(t.TempDir(), "not", "yet", "there")
	res := runScript(t, scriptPath, r, map[string]string{"KX_INSTALL_DIR": dir}, toolbox(t))
	if res.code != 0 {
		t.Fatalf("exit %d, stderr: %s", res.code, res.stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "kx")); err != nil {
		t.Errorf("want kx in %s: %v", dir, err)
	}
	mustNotExist(t, filepath.Join(res.home, ".local", "bin", "kx"))
}

func TestUnwritableInstallDir(t *testing.T) {
	hostPlatform(t)
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	r := newRelease(t, "v0.7.0", releaseOptions{})
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	res := runScript(t, scriptPath, r, map[string]string{"KX_INSTALL_DIR": dir}, toolbox(t))
	if res.code == 0 || !strings.Contains(res.stderr, "KX_INSTALL_DIR") {
		t.Fatalf("exit %d, stderr %q: want a refusal suggesting KX_INSTALL_DIR", res.code, res.stderr)
	}
	mustNotExist(t, filepath.Join(dir, "kx"))
}

func TestReplacesAnExistingInstall(t *testing.T) {
	hostPlatform(t)
	dir := t.TempDir()
	fakeCommand(t, dir, "kx", "echo 'kx v0.6.0'")
	r := newRelease(t, "v0.7.0", releaseOptions{})
	res := runScript(t, scriptPath, r, map[string]string{"KX_INSTALL_DIR": dir}, toolbox(t))
	if res.code != 0 {
		t.Fatalf("exit %d, stderr: %s", res.code, res.stderr)
	}
	out, _ := exec.Command(filepath.Join(dir, "kx"), "--version").Output()
	if strings.TrimSpace(string(out)) != "kx v0.7.0" {
		t.Errorf("after reinstall kx --version = %q, want kx v0.7.0", out)
	}
}

// Overwriting a running executable in place fails on Linux with "text file
// busy"; the script installs through a temp file and a rename, which does not.
// A shell-script stub can't show this — the kernel only guards mapped
// binaries — so this compiles a real one and keeps it running.
func TestReplacesARunningKx(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("text file busy is a Linux behavior")
	}
	hostPlatform(t)
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "main.go"),
		[]byte("package main\n\nimport \"time\"\n\nfunc main() { time.Sleep(time.Minute) }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "go.mod"), []byte("module sleeper\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	build := exec.Command("go", "build", "-o", filepath.Join(dir, "kx"), ".")
	build.Dir = src
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the stand-in kx: %v\n%s", err, out)
	}
	running := exec.Command(filepath.Join(dir, "kx"))
	if err := running.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { running.Process.Kill(); running.Wait() })

	r := newRelease(t, "v0.7.0", releaseOptions{})
	res := runScript(t, scriptPath, r, map[string]string{"KX_INSTALL_DIR": dir}, toolbox(t))
	if res.code != 0 {
		t.Fatalf("install over a running kx: exit %d, stderr: %s", res.code, res.stderr)
	}
	out, _ := exec.Command(filepath.Join(dir, "kx"), "--version").Output()
	if strings.TrimSpace(string(out)) != "kx v0.7.0" {
		t.Errorf("after install kx --version = %q, want kx v0.7.0", out)
	}
}

func TestRefusesTamperedArchive(t *testing.T) {
	hostPlatform(t)
	r := newRelease(t, "v0.7.0", releaseOptions{tamper: true})
	res := runScript(t, scriptPath, r, nil, toolbox(t))
	if res.code == 0 || !strings.Contains(res.stderr, "checksum mismatch") {
		t.Fatalf("exit %d, stderr %q: want a checksum refusal", res.code, res.stderr)
	}
	mustNotExist(t, filepath.Join(res.home, ".local", "bin", "kx"))
}

func TestRefusesUnlistedArchive(t *testing.T) {
	hostPlatform(t)
	r := newRelease(t, "v0.7.0", releaseOptions{unlisted: true})
	res := runScript(t, scriptPath, r, nil, toolbox(t))
	if res.code == 0 || !strings.Contains(res.stderr, "not listed") {
		t.Fatalf("exit %d, stderr %q: want a refusal for an unlisted archive", res.code, res.stderr)
	}
	mustNotExist(t, filepath.Join(res.home, ".local", "bin", "kx"))
}

func TestUnsupportedPlatform(t *testing.T) {
	for _, tc := range []struct{ kernel, machine string }{{"FreeBSD", "x86_64"}, {"Linux", "riscv64"}} {
		t.Run(tc.kernel+"/"+tc.machine, func(t *testing.T) {
			fakes := t.TempDir()
			fakeCommand(t, fakes, "uname", fmt.Sprintf(`case "$1" in -s) echo %s;; -m) echo %s;; esac`, tc.kernel, tc.machine))
			r := newRelease(t, "v0.7.0", releaseOptions{})
			res := runScript(t, scriptPath, r, nil, fakes, toolbox(t, "uname"))
			want := "no kx build for " + tc.kernel + "/" + tc.machine
			if res.code == 0 || !strings.Contains(res.stderr, want) || !strings.Contains(res.stderr, "uv tool install kx-cli") {
				t.Fatalf("exit %d, stderr %q: want %q and the uv alternative", res.code, res.stderr, want)
			}
			if len(r.paths()) != 0 {
				t.Errorf("requests = %v, want none for an unsupported platform", r.paths())
			}
		})
	}
}

func TestRosettaGetsNativeBuild(t *testing.T) {
	for _, tc := range []struct{ sysctl, want string }{{"1", "darwin_arm64"}, {"0", "darwin_amd64"}} {
		t.Run("hw.optional.arm64="+tc.sysctl, func(t *testing.T) {
			fakes := t.TempDir()
			fakeCommand(t, fakes, "uname", `case "$1" in -s) echo Darwin;; -m) echo x86_64;; esac`)
			fakeCommand(t, fakes, "sysctl", "echo "+tc.sysctl)
			r := newRelease(t, "v0.7.0", releaseOptions{})
			res := runScript(t, scriptPath, r, nil, fakes, toolbox(t, "uname", "sysctl"))
			if res.code != 0 {
				t.Fatalf("exit %d, stderr: %s", res.code, res.stderr)
			}
			if want := "/latest/download/kx_" + tc.want + ".tar.gz"; !contains(r.paths(), want) {
				t.Errorf("requests = %v, want %s", r.paths(), want)
			}
		})
	}
}

func TestPathHint(t *testing.T) {
	hostPlatform(t)
	r := newRelease(t, "v0.7.0", releaseOptions{})
	dir := t.TempDir()
	off := runScript(t, scriptPath, r, map[string]string{"KX_INSTALL_DIR": dir}, toolbox(t))
	if want := `export PATH="` + dir + `:$PATH"`; !strings.Contains(off.stdout, want) {
		t.Errorf("install dir off PATH: stdout = %q, want %q", off.stdout, want)
	}
	on := runScript(t, scriptPath, r, map[string]string{"KX_INSTALL_DIR": dir}, toolbox(t), dir)
	if strings.Contains(on.stdout, "not on your PATH") {
		t.Errorf("install dir on PATH: stdout = %q, want no hint", on.stdout)
	}
}

func TestNamesMissingChecksumTool(t *testing.T) {
	r := newRelease(t, "v0.7.0", releaseOptions{})
	res := runScript(t, scriptPath, r, nil, toolbox(t, "sha256sum", "shasum"))
	if res.code == 0 || !strings.Contains(res.stderr, "sha256sum or shasum") {
		t.Fatalf("exit %d, stderr %q: want the missing tools named", res.code, res.stderr)
	}
	if len(r.paths()) != 0 {
		t.Errorf("requests = %v, want none before the tools are present", r.paths())
	}
}

func TestFallsBackToWget(t *testing.T) {
	hostPlatform(t)
	if _, err := exec.LookPath("wget"); err != nil {
		t.Skip("wget not installed")
	}
	r := newRelease(t, "v0.7.0", releaseOptions{})
	res := runScript(t, scriptPath, r, nil, toolbox(t, "curl"))
	if res.code != 0 {
		t.Fatalf("exit %d, stderr: %s", res.code, res.stderr)
	}
}

// A download cut off partway must run nothing: no request, no directory. Every
// prefix of the script short of its last line is run as if it were the whole
// download.
func TestTruncatedScriptRunsNothing(t *testing.T) {
	full, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitAfter(string(full), "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if last := strings.TrimSpace(lines[len(lines)-1]); last != `main "$@"` {
		t.Fatalf("last line = %q, want main \"$@\"", last)
	}
	tools := toolbox(t)
	for n := 1; n < len(lines); n++ {
		prefix := filepath.Join(t.TempDir(), "install.sh")
		if err := os.WriteFile(prefix, []byte(strings.Join(lines[:n], "")), 0o644); err != nil {
			t.Fatal(err)
		}
		r := newRelease(t, "v0.7.0", releaseOptions{})
		res := runScript(t, prefix, r, nil, tools)
		if len(r.paths()) != 0 {
			t.Fatalf("first %d lines made requests %v", n, r.paths())
		}
		mustNotExist(t, filepath.Join(res.home, ".local"))
	}
}
