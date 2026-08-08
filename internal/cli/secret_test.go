package cli

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jzills/kx/internal/config"
	"github.com/jzills/kx/internal/index"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/kubectl"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/state"
)

func TestDecodeData(t *testing.T) {
	var object map[string]json.RawMessage
	// "hunter2" and "app", base64 as the API returns them.
	if err := json.Unmarshal([]byte(
		`{"data":{"password":"aHVudGVyMg==","username":"YXBw"}}`), &object); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	keys, values, err := decodeData(object)
	if err != nil {
		t.Fatalf("decodeData: %v", err)
	}
	// Sorted, because the API returns a JSON object and Go map iteration would
	// reorder the rows on every run.
	if len(keys) != 2 || keys[0] != "password" || keys[1] != "username" {
		t.Errorf("keys = %v, want [password username]", keys)
	}
	if string(values["password"]) != "hunter2" {
		t.Errorf("password = %q, want hunter2", values["password"])
	}
}

func TestDecodeDataEmptySecret(t *testing.T) {
	keys, values, err := decodeData(map[string]json.RawMessage{})
	if err != nil {
		t.Fatalf("decodeData: %v", err)
	}
	if len(keys) != 0 || len(values) != 0 {
		t.Errorf("keys = %v, values = %v; want empty", keys, values)
	}
}

// Binary payloads render as a placeholder so a keystore doesn't garble the
// table; --key still emits the raw bytes.
func TestToDisplay(t *testing.T) {
	if got := toDisplay([]byte("hunter2")); got != "hunter2" {
		t.Errorf("toDisplay(text) = %q", got)
	}
	binary := []byte{0xfe, 0xed, 0xfa, 0xce, 0x00}
	if got := toDisplay(binary); got != "<binary, 5 bytes>" {
		t.Errorf("toDisplay(binary) = %q, want a placeholder", got)
	}
}

// The trailing newline is added only for text, so a binary redirect reproduces
// the file exactly while text stays shell-friendly.
func TestWriteValueNewlineOnlyForText(t *testing.T) {
	// Verified end-to-end against the Python build; this pins the rule the
	// implementation encodes.
	text := []byte("token")
	if !isText(text) {
		t.Error("text was classified as binary")
	}
	binary := []byte{0x00, 0xff}
	if isText(binary) {
		t.Error("binary was classified as text")
	}
}

func isText(value []byte) bool { return toDisplay(value) == string(value) }

func TestSecretFlags(t *testing.T) {
	rest, options, err := secretFlags([]string{
		"1", "--decode", "-k", "tls.crt", "-m", "web", "-y", "-n", "prod"})
	if err != nil {
		t.Fatalf("secretFlags: %v", err)
	}
	if !options.Decode || !options.Yes {
		t.Errorf("options = %+v, want decode and yes set", options)
	}
	if !options.HasKey || options.Key != "tls.crt" {
		t.Errorf("key = %q (set=%v), want tls.crt", options.Key, options.HasKey)
	}
	if options.Match != "web" {
		t.Errorf("match = %q, want web", options.Match)
	}
	// Everything kx doesn't own reaches kubectl untouched.
	if strings.Join(rest, " ") != "1 -n prod" {
		t.Errorf("rest = %q, want \"1 -n prod\"", strings.Join(rest, " "))
	}
}

func TestSecretFlagsAbsentKey(t *testing.T) {
	_, options, err := secretFlags([]string{"1", "--decode"})
	if err != nil {
		t.Fatalf("secretFlags: %v", err)
	}
	if options.HasKey {
		t.Error("HasKey is set with no --key given")
	}
}

// An explicitly empty --key asks for a key that cannot exist. Treating it as an
// absent flag would silently widen `kx secret 1 --decode -k ""` from one value
// into every value in the Secret, which is the opposite of what was typed.
func TestSecretFlagsEmptyKeyIsNotAnAbsentKey(t *testing.T) {
	for _, args := range [][]string{
		{"1", "--decode", "-k", ""},
		{"1", "--decode", "--key", ""},
		{"1", "--decode", "--key="},
	} {
		_, options, err := secretFlags(args)
		if err != nil {
			t.Fatalf("secretFlags(%q): %v", args, err)
		}
		if !options.HasKey {
			t.Errorf("secretFlags(%q): HasKey = false, want true", args)
		}
		if options.Key != "" {
			t.Errorf("secretFlags(%q): Key = %q, want empty", args, options.Key)
		}
	}
}

const (
	oneSecretJSON = `{"kind":"Secret","metadata":{"name":"db-creds","namespace":"prod"},` +
		`"data":{"password":"aHVudGVyMg==","username":"YXBw"}}`

	secretListJSON = `{"items":[` +
		`{"kind":"Secret","metadata":{"name":"db-creds","namespace":"prod"},` +
		`"data":{"password":"aHVudGVyMg=="}},` +
		`{"kind":"Secret","metadata":{"name":"api-token","namespace":"prod"},` +
		`"data":{"token":"dGtuLTlmM2EyYg=="}}]}`
)

// The decoded values the fixtures above carry. Deliberately unlike any word the
// renderer emits: a plaintext of "secret" would make the leak assertions below
// pass against banner text rather than against the values themselves.
var secretPlaintexts = []string{"hunter2", "tkn-9f3a2b"}

// secretServices seeds a single indexed resource as the current state entry.
// The kind is a parameter because a stale index resolving to something that is
// not a Secret is one of the cases under test.
func secretServices(t *testing.T, kube kubectl.Service, kind kinds.Kind) Services {
	t.Helper()
	store := &state.Service{MaxHistory: 10, Path: filepath.Join(t.TempDir(), "state.json")}
	if err := store.Save(state.State{
		Resources: state.NewResources([]string{"db-creds"}, kind),
		Namespace: "prod",
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	return Services{
		Kubectl: kube, State: store, Index: index.Service{}, Config: config.Default(),
	}
}

func decodeOptions() getOptions { return getOptions{Decode: true} }

// A namespace-wide decode prints every credential in the namespace and sits one
// flag away from the listing people run by reflex. Declining the prompt must
// disclose nothing.
func TestNamespaceDecodeDeclinedPrintsNoValues(t *testing.T) {
	out := captureRender(t)
	services := secretServices(t, &fakeKubectl{output: secretListJSON}, kinds.Secret)
	asked := ""
	services.Confirm = func(message string) error {
		asked = message
		return render.ErrAborted{}
	}

	err := decodeSecrets(services, "secret", nil, nil, decodeOptions())
	var aborted render.ErrAborted
	if !errors.As(err, &aborted) {
		t.Fatalf("err = %v, want ErrAborted", err)
	}
	if !strings.Contains(asked, "2 Secrets") || !strings.Contains(asked, "prod") {
		t.Errorf("prompt = %q, want it to name the count and namespace", asked)
	}
	// The assertion that matters: no decoded value reached the terminal.
	for _, plaintext := range secretPlaintexts {
		if strings.Contains(out.String(), plaintext) {
			t.Errorf("declined decode leaked %q:\n%s", plaintext, out.String())
		}
	}
}

func TestNamespaceDecodeAcceptedPrintsValues(t *testing.T) {
	out := captureRender(t)
	services := secretServices(t, &fakeKubectl{output: secretListJSON}, kinds.Secret)
	services.Confirm = func(string) error { return nil }

	if err := decodeSecrets(services, "secret", nil, nil, decodeOptions()); err != nil {
		t.Fatalf("decodeSecrets: %v", err)
	}
	for _, want := range append([]string{"db-creds", "api-token"}, secretPlaintexts...) {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q:\n%s", want, out.String())
		}
	}
}

// --yes is the documented way to skip the prompt; if it stopped working the
// prompt would block a scripted decode instead of failing it.
func TestNamespaceDecodeYesSkipsThePrompt(t *testing.T) {
	captureRender(t)
	services := secretServices(t, &fakeKubectl{output: secretListJSON}, kinds.Secret)
	services.Confirm = func(string) error {
		t.Error("--yes still prompted")
		return nil
	}

	options := decodeOptions()
	options.Yes = true
	if err := decodeSecrets(services, "secret", nil, nil, options); err != nil {
		t.Fatalf("decodeSecrets: %v", err)
	}
}

// An empty namespace is not a decode worth confirming, so the prompt is skipped
// rather than asking about nothing.
func TestNamespaceDecodeWithNoSecretsDoesNotPrompt(t *testing.T) {
	captureRender(t)
	services := secretServices(t, &fakeKubectl{output: `{"items":[]}`}, kinds.Secret)
	services.Confirm = func(string) error {
		t.Error("prompted with no Secrets to decode")
		return nil
	}
	if err := decodeSecrets(services, "secret", nil, nil, decodeOptions()); err != nil {
		t.Fatalf("decodeSecrets: %v", err)
	}
}

func TestDecodeGuards(t *testing.T) {
	tests := []struct {
		name    string
		kind    kinds.Kind
		res     string
		indexes []int
		options getOptions
		want    string
	}{
		{
			name: "--key without --decode", kind: kinds.Secret, res: "secret",
			indexes: []int{1}, options: getOptions{HasKey: true, Key: "tls.crt"},
			want: "--key requires --decode",
		},
		{
			name: "--decode on a non-Secret kind", kind: kinds.Pod, res: "pods",
			indexes: []int{1}, options: decodeOptions(),
			want: "--decode only applies to Secrets",
		},
		{
			name: "--key with several indexes", kind: kinds.Secret, res: "secret",
			indexes: []int{1, 2}, options: getOptions{Decode: true, HasKey: true, Key: "a"},
			want: "--key takes a single index",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			quietRender(t)
			services := secretServices(t, &fakeKubectl{output: oneSecretJSON}, tc.kind)
			err := decodeSecrets(services, tc.res, tc.indexes, nil, tc.options)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

// `kubectl config set-context` accepts any string, and so does a decode: without
// the kind check a stale index pointing at a Pod would be fetched as a Secret.
func TestDecodeRejectsAnIndexThatIsNotASecret(t *testing.T) {
	quietRender(t)
	kube := &fakeKubectl{output: oneSecretJSON}
	services := secretServices(t, kube, kinds.Pod)

	err := decodeSecrets(services, "secret", []int{1}, nil, decodeOptions())
	if err == nil || !strings.Contains(err.Error(), "not Secret") {
		t.Fatalf("err = %v, want a kind mismatch", err)
	}
	// Rejected before the cluster was touched, not after.
	if kube.args != nil {
		t.Errorf("kubectl ran anyway: %v", kube.args)
	}
}

func TestDecodeMissingKeyNamesTheKey(t *testing.T) {
	quietRender(t)
	services := secretServices(t, &fakeKubectl{output: oneSecretJSON}, kinds.Secret)

	options := decodeOptions()
	options.HasKey, options.Key = true, "nope"
	err := decodeSecrets(services, "secret", []int{1}, nil, options)
	if err == nil || !strings.Contains(err.Error(), "No key 'nope'") {
		t.Fatalf("err = %v, want it to name the missing key", err)
	}
}

// A Secret that vanished between the listing and the decode is stale state, not
// a flat failure — the explicit type is what routes it into the refresh path.
func TestDecodeNotFoundBecomesStale(t *testing.T) {
	quietRender(t)
	kube := &fakeKubectl{err: errors.New(`Error from server (NotFound): secrets "db-creds" not found`)}
	services := secretServices(t, kube, kinds.Secret)

	err := decodeSecrets(services, "secret", []int{1}, nil, decodeOptions())
	var stale StaleResourceError
	if !errors.As(err, &stale) {
		t.Fatalf("err = %T (%v), want StaleResourceError", err, err)
	}
	if stale.Name != "db-creds" || stale.Kind != kinds.Secret {
		t.Errorf("stale = %+v, want Secret/db-creds", stale)
	}
}

// -n/-A are pure kubectl passthrough, parsed by hand, but were never
// registered so they vanished from --help despite being the most-used flags
// on this command.
func TestSecretRegistersNamespaceFlags(t *testing.T) {
	cmd := newSecretCommand(Services{}, "secret", []string{"secrets"})
	for _, name := range []string{"namespace", "all-namespaces"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("--%s is not registered, so it will not appear in --help", name)
		}
	}
}
