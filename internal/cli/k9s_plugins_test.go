package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// k9sPack is the plugin file the site publishes for k9s. It is not Go and no
// build reads it, so nothing but this test notices when a kx command it calls
// is renamed or stops taking a flag it passes.
const k9sPack = "../../site/static/k9s/kx.yaml"

type k9sPlugin struct {
	ShortCut    string   `yaml:"shortCut"`
	Description string   `yaml:"description"`
	Scopes      []string `yaml:"scopes"`
	Command     string   `yaml:"command"`
	Background  bool     `yaml:"background"`
	Args        []string `yaml:"args"`
}

func loadK9sPack(t *testing.T) map[string]k9sPlugin {
	t.Helper()
	raw, err := os.ReadFile(k9sPack)
	if err != nil {
		t.Fatalf("read %s: %v", k9sPack, err)
	}
	var file struct {
		Plugins map[string]k9sPlugin `yaml:"plugins"`
	}
	if err := yaml.Unmarshal(raw, &file); err != nil {
		t.Fatalf("parse %s: %v", k9sPack, err)
	}
	if len(file.Plugins) == 0 {
		t.Fatalf("%s declares no plugins", k9sPack)
	}
	return file.Plugins
}

// Every plugin runs its script the way k9s does — sh -c, with k9s's
// variables already substituted — against a kx stub that records its argv,
// and that argv has to be one kx accepts: a known command, and flags that
// command takes. A row with no namespace (a Node) must not pass an empty -n.
func TestK9sPluginsRunCommandsKxAccepts(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh to run the plugin scripts with")
	}
	stubDir := t.TempDir()
	calls := filepath.Join(stubDir, "calls")
	stub := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + calls + "\n"
	if err := os.WriteFile(filepath.Join(stubDir, "kx"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	keys := map[string]string{}
	for name, plugin := range loadK9sPack(t) {
		if other, taken := keys[plugin.ShortCut]; taken {
			t.Errorf("%s and %s both bind %s", name, other, plugin.ShortCut)
		}
		keys[plugin.ShortCut] = name
		if plugin.Command != "sh" || len(plugin.Args) != 7 || plugin.Args[0] != "-c" || plugin.Background {
			t.Errorf("%s: want a foreground sh -c script taking kind, name, namespace and context; got %+v", name, plugin)
			continue
		}
		if got := strings.Join(plugin.Args[2:], " "); got != "kx-k9s $RESOURCE_NAME $NAME $NAMESPACE $CONTEXT" {
			t.Errorf("%s: script arguments are %q", name, got)
			continue
		}
		for _, row := range []struct {
			resource, name, namespace string
			wantNamespace             bool
		}{
			{"deployments", "api", "prod", true},
			{"nodes", "worker", "", false},
		} {
			os.Remove(calls)
			script := exec.Command("sh", "-c", plugin.Args[1], "kx-k9s",
				row.resource, row.name, row.namespace, "staging")
			script.Env = append(os.Environ(), "PATH="+stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			script.Stdin = strings.NewReader("api\n\n")
			if out, err := script.CombinedOutput(); err != nil {
				t.Errorf("%s on %s: script failed: %v\n%s", name, row.resource, err, out)
				continue
			}
			recorded, err := os.ReadFile(calls)
			if err != nil {
				t.Errorf("%s on %s: kx was never called", name, row.resource)
				continue
			}
			argv := strings.Fields(strings.TrimSpace(string(recorded)))
			target := row.resource + "/" + row.name
			if !strings.Contains(" "+strings.Join(argv, " ")+" ", " "+target+" ") {
				t.Errorf("%s on %s: kx got %v, want the row as %s", name, row.resource, argv, target)
			}
			if hasNamespace := strings.Contains(strings.Join(argv, " "), "-n "); hasNamespace != row.wantNamespace {
				t.Errorf("%s on %s: kx got %v; -n present = %v, want %v",
					name, row.resource, argv, hasNamespace, row.wantNamespace)
			}
			root := NewRoot(argvServices(t), "test")
			cmd, rest, err := root.Find(argv)
			if err != nil || cmd == root {
				t.Errorf("%s: kx has no command for %v", name, argv)
				continue
			}
			if err := cmd.ParseFlags(rest); err != nil {
				t.Errorf("%s: kx %s refuses the plugin's flags %v: %v", name, cmd.Name(), rest, err)
			}
		}
	}
}

// k9sBuiltinShiftKeys are the Shift bindings k9s v0.51.0's own help screen
// lists in the views the pack is scoped to (pods, deployments, services,
// nodes): the sorts, Jump Owner, Port-Forward, and Goto Bottom under
// NAVIGATION. A plugin on one of them takes the key away from k9s, which is
// how tree came to sit on Goto Bottom — the menu bar the pack was first
// checked against leaves the navigation keys out.
var k9sBuiltinShiftKeys = []string{"Shift-A", "Shift-F", "Shift-G", "Shift-J", "Shift-N", "Shift-O", "Shift-S"}

func TestK9sPluginsLeaveK9sOwnKeysAlone(t *testing.T) {
	for name, plugin := range loadK9sPack(t) {
		for _, builtin := range k9sBuiltinShiftKeys {
			if strings.EqualFold(plugin.ShortCut, builtin) {
				t.Errorf("%s binds %s, which k9s already uses", name, plugin.ShortCut)
			}
		}
	}
}
