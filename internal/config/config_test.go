package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, contents string) Loader {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return Loader{Path: path}
}

// KX_CONFIG redirects File() itself — the one thing Loader.Path can't stand
// in for, since a caller with no Path relies on File() to find ~/.kx by
// default. Without this, two shells always shared one config.toml.
func TestFileHonorsKXConfigOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "custom.toml")
	t.Setenv("KX_CONFIG", path)
	got, err := File()
	if err != nil {
		t.Fatalf("File: %v", err)
	}
	if got != path {
		t.Errorf("File() = %q, want %q", got, path)
	}
}

// An empty KX_CONFIG must fall back to the default rather than trying to open
// "" as a path — the same rule os.LookupEnv's ok/empty distinction exists for.
func TestFileEmptyKXConfigFallsBackToDefault(t *testing.T) {
	t.Setenv("KX_CONFIG", "")
	got, err := File()
	if err != nil {
		t.Fatalf("File: %v", err)
	}
	if !strings.HasSuffix(got, filepath.Join(".kx", "config.toml")) {
		t.Errorf("File() = %q, want the default ~/.kx/config.toml path", got)
	}
}

func TestDefaultsWhenNoFile(t *testing.T) {
	loader := Loader{Path: filepath.Join(t.TempDir(), "absent.toml")}
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MaxHistory != 10 || cfg.Theme != DefaultTheme || cfg.ThemeDisable || cfg.Engine != DefaultEngine {
		t.Errorf("defaults = %+v", cfg)
	}
	if len(cfg.Shells) != 2 || cfg.Shells[0] != "bash" || cfg.Shells[1] != "sh" {
		t.Errorf("Shells = %v, want [bash sh]", cfg.Shells)
	}
}

func TestLoadsFromFile(t *testing.T) {
	loader := writeConfig(t, `
max_history = 3
shells = ["zsh", "bash"]
theme_disable = true
theme = "solarized"
engine = "trivy"
`)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MaxHistory != 3 || !cfg.ThemeDisable || cfg.Theme != "solarized" || cfg.Engine != "trivy" {
		t.Errorf("cfg = %+v", cfg)
	}
	if len(cfg.Shells) != 2 || cfg.Shells[0] != "zsh" {
		t.Errorf("Shells = %v, want [zsh bash]", cfg.Shells)
	}
}

func TestEnvironmentOverridesFile(t *testing.T) {
	loader := writeConfig(t, "max_history = 3\ntheme = \"solarized\"\nengine = \"trivy\"\n")
	t.Setenv("KX_MAX_HISTORY", "7")
	t.Setenv("KX_THEME", "dracula")
	t.Setenv("KX_SHELLS", "fish,sh")
	t.Setenv("KX_THEME_DISABLE", "yes")
	t.Setenv("KX_ENGINE", "scout")

	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MaxHistory != 7 || cfg.Theme != "dracula" || !cfg.ThemeDisable || cfg.Engine != "scout" {
		t.Errorf("cfg = %+v", cfg)
	}
	if len(cfg.Shells) != 2 || cfg.Shells[0] != "fish" {
		t.Errorf("Shells = %v, want [fish sh]", cfg.Shells)
	}
}

func TestThemeDisableEnvOffValues(t *testing.T) {
	loader := writeConfig(t, "theme_disable = true\n")
	t.Setenv("KX_THEME_DISABLE", "0")
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ThemeDisable {
		t.Error("KX_THEME_DISABLE=0 did not turn styling back on")
	}
}

// The setting was called no_color / KX_NO_COLOR before it was grouped with
// KX_THEME. Nothing reads the old spellings any more — a config file still
// holding one is a file kx styles output against, silently.
func TestOldNoColorSpellingsAreNotRead(t *testing.T) {
	loader := writeConfig(t, "no_color = true\n")
	t.Setenv("KX_NO_COLOR", "1")
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ThemeDisable {
		t.Error("Load still honours the old no_color spelling")
	}
}

// A bool is not an acceptable max_history, and neither is a string or a
// non-positive number.
func TestInvalidMaxHistoryRejected(t *testing.T) {
	for name, contents := range map[string]string{
		"boolean":  "max_history = true",
		"string":   `max_history = "10"`,
		"zero":     "max_history = 0",
		"negative": "max_history = -1",
	} {
		t.Run(name, func(t *testing.T) {
			loader := writeConfig(t, contents)
			if _, err := loader.Load(); err == nil {
				t.Errorf("Load accepted max_history = %s", contents)
			}
		})
	}
}

func TestNonIntegerEnvMaxHistoryRejected(t *testing.T) {
	loader := Loader{Path: filepath.Join(t.TempDir(), "absent.toml")}
	t.Setenv("KX_MAX_HISTORY", "many")
	_, err := loader.Load()
	if err == nil || !strings.Contains(err.Error(), "must be an integer") {
		t.Errorf("error = %v, want an integer complaint", err)
	}
}

func TestMalformedTOMLReportsPath(t *testing.T) {
	loader := writeConfig(t, "max_history = [unclosed")
	_, err := loader.Load()
	if err == nil {
		t.Fatal("Load accepted malformed TOML")
	}
	if !strings.Contains(err.Error(), loader.Path) {
		t.Errorf("error = %q, want it to name the config file", err)
	}
}

func TestUnknownThemeRejectedWhenValidatorSupplied(t *testing.T) {
	loader := writeConfig(t, `theme = "nonexistent"`)
	loader.ThemeKnown = func(string) bool { return false }
	_, err := loader.Load()
	if err == nil || !strings.Contains(err.Error(), "unknown theme") {
		t.Errorf("error = %v, want an unknown-theme complaint", err)
	}
}

// Rewriting only the theme line keeps user comments and formatting intact.
func TestSaveThemeReplacesExistingLinePreservingComments(t *testing.T) {
	loader := writeConfig(t, "# my settings\nmax_history = 3\ntheme = \"old\"\n")
	if err := loader.SaveTheme("new"); err != nil {
		t.Fatalf("SaveTheme: %v", err)
	}

	data, err := os.ReadFile(loader.Path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, `theme = "new"`) {
		t.Errorf("theme not updated:\n%s", text)
	}
	if strings.Contains(text, `"old"`) {
		t.Errorf("old theme still present:\n%s", text)
	}
	if !strings.Contains(text, "# my settings") || !strings.Contains(text, "max_history = 3") {
		t.Errorf("SaveTheme clobbered unrelated config:\n%s", text)
	}
}

func TestSaveThemeAppendsWhenAbsent(t *testing.T) {
	loader := writeConfig(t, "max_history = 3\n")
	if err := loader.SaveTheme("new"); err != nil {
		t.Fatalf("SaveTheme: %v", err)
	}

	data, _ := os.ReadFile(loader.Path)
	if !strings.Contains(string(data), `theme = "new"`) {
		t.Errorf("theme not appended:\n%s", data)
	}
	if !strings.Contains(string(data), "max_history = 3") {
		t.Errorf("SaveTheme clobbered existing config:\n%s", data)
	}
}

func TestSaveThemeCreatesFile(t *testing.T) {
	loader := Loader{Path: filepath.Join(t.TempDir(), "nested", "config.toml")}
	if err := loader.SaveTheme("new"); err != nil {
		t.Fatalf("SaveTheme: %v", err)
	}

	data, err := os.ReadFile(loader.Path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), `theme = "new"`) {
		t.Errorf("file contents = %q", data)
	}
}

func TestLoadAfterSaveTheme(t *testing.T) {
	loader := writeConfig(t, "max_history = 3\n")
	if err := loader.SaveTheme("persisted"); err != nil {
		t.Fatalf("SaveTheme: %v", err)
	}
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Theme != "persisted" {
		t.Errorf("Theme = %q, want persisted", cfg.Theme)
	}
}

func TestUnknownEngineRejectedWhenValidatorSupplied(t *testing.T) {
	loader := writeConfig(t, `engine = "nonexistent"`)
	loader.EngineKnown = func(string) bool { return false }
	_, err := loader.Load()
	if err == nil || !strings.Contains(err.Error(), "unknown engine") {
		t.Errorf("error = %v, want an unknown-engine complaint", err)
	}
}

func TestSaveEngineReplacesExistingLinePreservingComments(t *testing.T) {
	loader := writeConfig(t, "# my settings\nmax_history = 3\nengine = \"scout\"\n")
	if err := loader.SaveEngine("trivy"); err != nil {
		t.Fatalf("SaveEngine: %v", err)
	}

	data, err := os.ReadFile(loader.Path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, `engine = "trivy"`) {
		t.Errorf("engine not updated:\n%s", text)
	}
	if strings.Contains(text, `"scout"`) {
		t.Errorf("old engine still present:\n%s", text)
	}
	if !strings.Contains(text, "# my settings") || !strings.Contains(text, "max_history = 3") {
		t.Errorf("SaveEngine clobbered unrelated config:\n%s", text)
	}
}

func TestSaveEngineAppendsWhenAbsent(t *testing.T) {
	loader := writeConfig(t, "max_history = 3\n")
	if err := loader.SaveEngine("trivy"); err != nil {
		t.Fatalf("SaveEngine: %v", err)
	}

	data, _ := os.ReadFile(loader.Path)
	if !strings.Contains(string(data), `engine = "trivy"`) {
		t.Errorf("engine not appended:\n%s", data)
	}
	if !strings.Contains(string(data), "max_history = 3") {
		t.Errorf("SaveEngine clobbered existing config:\n%s", data)
	}
}

func TestSaveEngineCreatesFile(t *testing.T) {
	loader := Loader{Path: filepath.Join(t.TempDir(), "nested", "config.toml")}
	if err := loader.SaveEngine("trivy"); err != nil {
		t.Fatalf("SaveEngine: %v", err)
	}

	data, err := os.ReadFile(loader.Path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), `engine = "trivy"`) {
		t.Errorf("file contents = %q", data)
	}
}

func TestLoadAfterSaveEngine(t *testing.T) {
	loader := writeConfig(t, "max_history = 3\n")
	if err := loader.SaveEngine("trivy"); err != nil {
		t.Fatalf("SaveEngine: %v", err)
	}
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Engine != "trivy" {
		t.Errorf("Engine = %q, want trivy", cfg.Engine)
	}
}

// Settings is what `kx --help` lists under Environment, so an override Load
// honours but Settings omits is one a user can only discover by reading the
// source. The loader's own text is the reference: every KX_* variable it looks
// up must be documented.
func TestSettingsDocumentsEveryEnvOverride(t *testing.T) {
	source, err := os.ReadFile("config.go")
	if err != nil {
		t.Fatalf("ReadFile(config.go): %v", err)
	}

	documented := map[string]bool{}
	for _, setting := range Settings() {
		documented[setting.Env] = true
	}

	// LookupEnv is how Load reads every override; the literal beside it is the
	// variable name.
	lookups := regexp.MustCompile(`LookupEnv\("(KX_[A-Z_]+)"\)`).FindAllStringSubmatch(string(source), -1)
	if len(lookups) == 0 {
		t.Fatal("found no LookupEnv calls in config.go; this test can no longer see what Load reads")
	}
	for _, lookup := range lookups {
		if !documented[lookup[1]] {
			t.Errorf("Load reads %s, but Settings() does not document it", lookup[1])
		}
	}
}

// kx debug attaches a container of the user's choosing, and someone who
// standardises on their own toolbox image should say so once rather than pass
// --image every time — the same reason `shells` exists for kx exec.
func TestDebugImageDefaults(t *testing.T) {
	if got := Default().DebugImage; got != "busybox" {
		t.Errorf("DebugImage default = %q, want busybox", got)
	}
}

func TestDebugImageFromFile(t *testing.T) {
	cfg, err := writeConfig(t, "debug_image = \"ghcr.io/me/toolbox:v1\"\n").Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.DebugImage != "ghcr.io/me/toolbox:v1" {
		t.Errorf("DebugImage = %q, want the configured image", cfg.DebugImage)
	}
}

func TestDebugImageEnvOverridesFile(t *testing.T) {
	t.Setenv("KX_DEBUG_IMAGE", "alpine:3.20")

	cfg, err := writeConfig(t, "debug_image = \"ghcr.io/me/toolbox:v1\"\n").Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.DebugImage != "alpine:3.20" {
		t.Errorf("DebugImage = %q, want the environment override", cfg.DebugImage)
	}
}

func TestDebugImageRejectsANonString(t *testing.T) {
	if _, err := writeConfig(t, "debug_image = 3\n").Load(); err == nil {
		t.Error("a numeric debug_image was accepted")
	}
}

// Unbounded unless asked: kx diag reports what it always reported until
// someone chooses a window, so an upgrade changes no verdict and no exit
// code on its own.
func TestDiagMaxAgeDefaultsToNoWindow(t *testing.T) {
	loader := Loader{Path: filepath.Join(t.TempDir(), "absent.toml")}
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DiagMaxAge != 0 {
		t.Errorf("DiagMaxAge = %v, want no window", cfg.DiagMaxAge)
	}
}

func TestDiagMaxAgeFromFile(t *testing.T) {
	loader := writeConfig(t, "diag_max_age = \"7d\"\n")
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if want := 7 * 24 * time.Hour; cfg.DiagMaxAge != want {
		t.Errorf("DiagMaxAge = %v, want %v", cfg.DiagMaxAge, want)
	}
}

func TestDiagMaxAgeEnvOverridesFile(t *testing.T) {
	loader := writeConfig(t, "diag_max_age = \"7d\"\n")
	t.Setenv("KX_DIAG_MAX_AGE", "30m")
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if want := 30 * time.Minute; cfg.DiagMaxAge != want {
		t.Errorf("DiagMaxAge = %v, want %v", cfg.DiagMaxAge, want)
	}
}

// Zero is how the file spells "no window", so it has to reach the diagnostics
// rather than being read as an unset key falling back to the default.
func TestDiagMaxAgeZeroIsUnlimited(t *testing.T) {
	loader := writeConfig(t, "diag_max_age = \"0\"\n")
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DiagMaxAge != 0 {
		t.Errorf("DiagMaxAge = %v, want 0", cfg.DiagMaxAge)
	}
}

// A duration is a string in TOML — `diag_max_age = 7` has no unit and cannot
// be guessed at.
func TestDiagMaxAgeRejectsANonString(t *testing.T) {
	loader := writeConfig(t, "diag_max_age = 7\n")
	if _, err := loader.Load(); err == nil {
		t.Fatal("Load() = nil error for a non-string diag_max_age")
	} else if !strings.Contains(err.Error(), "diag_max_age") {
		t.Errorf("error = %q, want it to name diag_max_age", err)
	}
}

func TestDiagMaxAgeRejectsAMalformedValue(t *testing.T) {
	loader := writeConfig(t, "diag_max_age = \"7 weeks\"\n")
	if _, err := loader.Load(); err == nil {
		t.Fatal("Load() = nil error for a malformed diag_max_age")
	} else if !strings.HasPrefix(err.Error(), "kx: ") ||
		!strings.Contains(err.Error(), "diag_max_age") {
		t.Errorf("error = %q, want a kx: error naming diag_max_age", err)
	}
}

func TestDiagMaxAgeRejectsAMalformedEnvValue(t *testing.T) {
	loader := Loader{Path: filepath.Join(t.TempDir(), "absent.toml")}
	t.Setenv("KX_DIAG_MAX_AGE", "later")
	if _, err := loader.Load(); err == nil {
		t.Fatal("Load() = nil error for a malformed KX_DIAG_MAX_AGE")
	} else if !strings.Contains(err.Error(), "KX_DIAG_MAX_AGE") {
		t.Errorf("error = %q, want it to name KX_DIAG_MAX_AGE", err)
	}
}
