// Package config loads ~/.kx/config.toml and the KX_* environment overrides.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// DefaultTheme is the palette used when none is configured. It lives here until
// the theme registry lands, at which point config resolves it from there.
const DefaultTheme = "github-dark"

// DefaultEngine is the scan engine used when none is configured. Mirrors
// DefaultTheme: a hardcoded literal here rather than importing scanner, same
// stopgap as DefaultTheme not importing theme.
const DefaultEngine = "scout"

// DefaultDebugImage is the image kx debug attaches when none is configured.
// Small, ubiquitous, and carries a shell — which is the whole point, since the
// pod being debugged is one whose own image has none.
const DefaultDebugImage = "busybox"

// Config is the resolved configuration. Defaults match the Python
// implementation's dataclass defaults.
type Config struct {
	MaxHistory int
	Shells     []string
	NoColor    bool
	Theme      string
	Engine     string
	DebugImage string
}

// Default returns the configuration used when nothing is set.
func Default() Config {
	return Config{
		MaxHistory: 10,
		Shells:     []string{"bash", "sh"},
		NoColor:    false,
		Theme:      DefaultTheme,
		Engine:     DefaultEngine,
		DebugImage: DefaultDebugImage,
	}
}

// Setting documents one configuration key: its name in config.toml, the
// environment variable that overrides it, and what it controls.
//
// Exported so the help screen can list what kx reads without keeping its own
// copy of the list. Only the documentation is shared — each key's parsing stays
// in Load, since no two of them parse alike.
type Setting struct {
	Key string
	Env string
	Doc string
}

// Settings is every key kx reads, in the order the help screen lists them.
//
// TestSettingsDocumentsEveryEnvOverride keeps this in step with Load: an
// override the loader honours but this list omits is one a user can only find
// by reading the source.
func Settings() []Setting {
	return []Setting{
		{"theme", "KX_THEME", "Color theme for all output; see kx theme"},
		{"engine", "KX_ENGINE", "Default scan engine for kx scan; see kx engine"},
		{"max_history", "KX_MAX_HISTORY", "Number of kx get results kept in history"},
		{"shells", "KX_SHELLS", "Shell candidates for kx exec, comma-separated"},
		{"debug_image", "KX_DEBUG_IMAGE", "Image kx debug attaches to a pod"},
		{"no_color", "KX_NO_COLOR", "Disable styled output, like --no-color"},
	}
}

// File returns the config file path.
func File() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot locate home directory: %w", err)
	}
	return filepath.Join(home, ".kx", "config.toml"), nil
}

// Loader reads configuration from a specific file, so tests don't depend on the
// caller's home directory.
type Loader struct {
	// Path is the config file. Empty means ~/.kx/config.toml.
	Path string
	// ThemeKnown validates a configured theme name. Nil skips validation,
	// which is what callers do before the theme registry exists.
	ThemeKnown func(string) bool
	// EngineKnown validates a configured scan engine name. Nil skips
	// validation, mirroring ThemeKnown.
	EngineKnown func(string) bool
}

func (l Loader) path() (string, error) {
	if l.Path != "" {
		return l.Path, nil
	}
	return File()
}

// Load resolves the config file then the environment, with the environment
// winning. Errors are already user-facing: they carry the "kx: " prefix and are
// meant to be printed verbatim.
func (l Loader) Load() (Config, error) {
	cfg := Default()

	path, err := l.path()
	if err != nil {
		return cfg, err
	}

	if data, err := os.ReadFile(path); err == nil {
		var raw map[string]any
		if err := decodeTOML(data, &raw); err != nil {
			return cfg, fmt.Errorf("kx: error reading %s: %w", path, err)
		}
		if value, ok := raw["max_history"]; ok {
			n, err := asPositiveInt(value)
			if err != nil {
				return cfg, err
			}
			cfg.MaxHistory = n
		}
		if value, ok := raw["shells"]; ok {
			shells, err := asStringSlice(value)
			if err != nil {
				return cfg, err
			}
			cfg.Shells = shells
		}
		if value, ok := raw["no_color"]; ok {
			flag, ok := value.(bool)
			if !ok {
				return cfg, errors.New("kx: no_color must be a boolean")
			}
			cfg.NoColor = flag
		}
		if value, ok := raw["theme"]; ok {
			name, ok := value.(string)
			if !ok {
				return cfg, errors.New("kx: theme must be a string")
			}
			cfg.Theme = name
		}
		if value, ok := raw["engine"]; ok {
			name, ok := value.(string)
			if !ok {
				return cfg, errors.New("kx: engine must be a string")
			}
			cfg.Engine = name
		}
		if value, ok := raw["debug_image"]; ok {
			name, ok := value.(string)
			if !ok {
				return cfg, errors.New("kx: debug_image must be a string")
			}
			cfg.DebugImage = name
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return cfg, fmt.Errorf("kx: error reading %s: %w", path, err)
	}

	if value, ok := os.LookupEnv("KX_MAX_HISTORY"); ok {
		n, err := strconv.Atoi(value)
		if err != nil {
			return cfg, errors.New("kx: KX_MAX_HISTORY must be an integer")
		}
		if n < 1 {
			return cfg, errors.New("kx: max_history must be a positive integer")
		}
		cfg.MaxHistory = n
	}
	if value, ok := os.LookupEnv("KX_SHELLS"); ok {
		cfg.Shells = strings.Split(value, ",")
	}
	if value, ok := os.LookupEnv("KX_NO_COLOR"); ok {
		switch strings.ToLower(value) {
		case "1", "true", "yes", "on":
			cfg.NoColor = true
		default:
			cfg.NoColor = false
		}
	}
	if value, ok := os.LookupEnv("KX_THEME"); ok {
		cfg.Theme = value
	}
	if value, ok := os.LookupEnv("KX_ENGINE"); ok {
		cfg.Engine = value
	}
	if value, ok := os.LookupEnv("KX_DEBUG_IMAGE"); ok {
		cfg.DebugImage = value
	}

	if l.ThemeKnown != nil && !l.ThemeKnown(cfg.Theme) {
		return cfg, fmt.Errorf(
			"kx: unknown theme '%s' (run kx theme to list themes)", cfg.Theme,
		)
	}
	if l.EngineKnown != nil && !l.EngineKnown(cfg.Engine) {
		return cfg, fmt.Errorf(
			"kx: unknown engine '%s' (run kx engine to list engines)", cfg.Engine,
		)
	}
	return cfg, nil
}

// asPositiveInt rejects booleans and non-positive values, matching the Python
// check that guards against `max_history = true` and `max_history = "10"`.
func asPositiveInt(value any) (int, error) {
	invalid := errors.New("kx: max_history must be a positive integer")
	switch n := value.(type) {
	case int64:
		if n < 1 {
			return 0, invalid
		}
		return int(n), nil
	case int:
		if n < 1 {
			return 0, invalid
		}
		return n, nil
	default:
		return 0, invalid
	}
}

func asStringSlice(value any) ([]string, error) {
	items, ok := value.([]any)
	if !ok {
		return nil, errors.New("kx: shells must be an array of strings")
	}
	shells := make([]string, 0, len(items))
	for _, item := range items {
		shell, ok := item.(string)
		if !ok {
			return nil, errors.New("kx: shells must be an array of strings")
		}
		shells = append(shells, shell)
	}
	return shells, nil
}

var themeLineRE = regexp.MustCompile(`(?m)^theme\s*=.*$`)

// SaveTheme persists the theme choice.
//
// Rewrites only the `theme = ...` line (or appends one) instead of
// round-tripping the TOML, so user comments and formatting survive. Safe
// because the config schema is flat: there are no tables a `theme` key could
// belong to.
func (l Loader) SaveTheme(name string) error {
	path, err := l.path()
	if err != nil {
		return err
	}
	line := fmt.Sprintf("theme = %q", name)

	existing, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, []byte(line+"\n"), 0o644)
	}
	if err != nil {
		return err
	}

	text := string(existing)
	if themeLineRE.MatchString(text) {
		text = replaceFirst(themeLineRE, text, line)
	} else {
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += line + "\n"
	}
	return os.WriteFile(path, []byte(text), 0o644)
}

var engineLineRE = regexp.MustCompile(`(?m)^engine\s*=.*$`)

// SaveEngine persists the default scan engine choice, mirroring SaveTheme.
func (l Loader) SaveEngine(name string) error {
	path, err := l.path()
	if err != nil {
		return err
	}
	line := fmt.Sprintf("engine = %q", name)

	existing, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, []byte(line+"\n"), 0o644)
	}
	if err != nil {
		return err
	}

	text := string(existing)
	if engineLineRE.MatchString(text) {
		text = replaceFirst(engineLineRE, text, line)
	} else {
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += line + "\n"
	}
	return os.WriteFile(path, []byte(text), 0o644)
}

// replaceFirst replaces only the first match, which regexp doesn't offer
// directly (Python's re.subn takes count=1).
func replaceFirst(re *regexp.Regexp, text, replacement string) string {
	loc := re.FindStringIndex(text)
	if loc == nil {
		return text
	}
	return text[:loc[0]] + replacement + text[loc[1]:]
}
