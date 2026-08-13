package cli

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"unicode/utf8"

	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/kubectl"
	"github.com/jzills/kx/internal/render"
	"github.com/spf13/cobra"
)

// SecretCommand reads and decodes Secret data.
type SecretCommand struct {
	Kubectl kubectl.Service
	State   IndexResolver
}

// secretData is one Secret's decoded contents. Values stay as bytes so the
// render layer owns the text/binary decision.
type secretData struct {
	Name      string
	Namespace string
	Keys      []string
	Values    map[string][]byte
}

// decodeData decodes a Secret object's `data` map.
//
// `stringData` is write-only and never returned by the API, so `data` holds
// everything.
func decodeData(object map[string]json.RawMessage) ([]string, map[string][]byte, error) {
	encoded := map[string]string{}
	if raw, ok := object["data"]; ok {
		if err := json.Unmarshal(raw, &encoded); err != nil {
			return nil, nil, err
		}
	}
	keys := make([]string, 0, len(encoded))
	values := make(map[string][]byte, len(encoded))
	for key, value := range encoded {
		decoded, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return nil, nil, fmt.Errorf("could not decode key '%s': %w", key, err)
		}
		keys = append(keys, key)
		values[key] = decoded
	}
	// Sorted for stable output: the API returns a JSON object and Go map
	// iteration would reorder the rows on every run.
	sort.Strings(keys)
	return keys, values, nil
}

// Execute returns the decoded data for one indexed Secret.
func (c SecretCommand) Execute(index int) (secretData, error) {
	name, namespace, kind, err := c.State.Fields(index)
	if err != nil {
		return secretData{}, err
	}
	raw, err := c.Kubectl.Run([]string{"get", string(kind), name, "-n", namespace, "-o", "json"})
	if err != nil {
		return secretData{}, err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &object); err != nil {
		return secretData{}, err
	}
	keys, values, err := decodeData(object)
	if err != nil {
		return secretData{}, err
	}
	return secretData{Name: name, Namespace: namespace, Keys: keys, Values: values}, nil
}

// ExecuteAll returns every Secret in the target namespace.
//
// One kubectl call returns the whole list with data attached, so a namespace
// sweep costs the same as a single fetch. extraArgs carries the user's kubectl
// flags so `-n` selects the namespace.
func (c SecretCommand) ExecuteAll(extraArgs []string) ([]secretData, error) {
	raw, err := c.Kubectl.Run(append([]string{"get", "secret", "-o", "json"}, extraArgs...))
	if err != nil {
		return nil, err
	}
	var list struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return nil, err
	}

	secrets := make([]secretData, 0, len(list.Items))
	for _, item := range list.Items {
		metadata := decodeObject(item["metadata"])
		keys, values, err := decodeData(item)
		if err != nil {
			return nil, err
		}
		secrets = append(secrets, secretData{
			Name:      decodeString(metadata["name"]),
			Namespace: decodeString(metadata["namespace"]),
			Keys:      keys,
			Values:    values,
		})
	}
	return secrets, nil
}

func decodeString(raw json.RawMessage) string {
	var value string
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &value)
	}
	return value
}

// toDisplay renders a value as text, or a placeholder when it isn't valid
// UTF-8 — which keeps keystores and other binary payloads from garbling the
// table. `--key` still emits raw bytes so they can be redirected to a file.
func toDisplay(value []byte) string {
	if !utf8.Valid(value) {
		return fmt.Sprintf("<binary, %d bytes>", len(value))
	}
	return string(value)
}

// writeValue writes one value to stdout unstyled, bypassing the renderer.
//
// The renderer would wrap at the console width, injecting newlines into a long
// value — a cert or a token — and corrupting `$(kx secret 1 --decode -k
// tls.crt)`. Writing bytes directly also keeps `> store.p12` byte-exact. The
// trailing newline is added only for text, so a binary redirect reproduces the
// file exactly while text stays shell-friendly.
func writeValue(value []byte) error {
	payload := value
	if utf8.Valid(value) {
		payload = append(append([]byte{}, value...), '\n')
	}
	_, err := os.Stdout.Write(payload)
	return err
}

// renderSecret prints one Secret's decoded data.
//
// No key count, unlike kx labels: a Secret holds a handful of keys, all visible
// in the table immediately below, so a count restates what the reader already
// has. Under a sweep the namespace is blank — the scope banner already named it.
func renderSecret(secret secretData, namespace string) {
	render.Banner(string(kinds.Secret), secret.Name, namespace, "")
	display := make(map[string]string, len(secret.Keys))
	for _, key := range secret.Keys {
		display[key] = toDisplay(secret.Values[key])
	}
	render.KeyValueTable("KEY", secret.Keys, display)
}

// decodeSecrets renders Secret data in plaintext: one indexed Secret, several,
// one key's raw value, or — with no index — every Secret in the namespace.
//
// Split out of the listing path because decoding reads resources rather than
// listing them, so it never re-saves state.
func decodeSecrets(services Services, resource string, indexes []int, extra []string, options getOptions) error {
	if !options.Decode {
		return fmt.Errorf("--key requires --decode")
	}
	expected := kinds.Normalize(resource)
	if expected != kinds.Secret {
		return fmt.Errorf("--decode only applies to Secrets, not %s", expected)
	}
	if options.HasKey && len(indexes) != 1 {
		return fmt.Errorf("--key takes a single index")
	}

	command := SecretCommand{Kubectl: services.Kubectl, State: services.State}
	if len(indexes) == 0 {
		return decodeNamespace(services, command, extra, options.Yes)
	}

	for position, index := range indexes {
		name, namespace, err := services.State.FieldsExpecting(index, expected)
		if err != nil {
			return err
		}

		stop := render.Status("fetching secret")
		secret, err := command.Execute(index)
		stop()
		if err != nil {
			// A NotFound here means the saved index outlived the Secret; the
			// explicit type triggers the refresh path.
			if IsNotFound(err) {
				return StaleResourceError{Kind: expected, Name: name}
			}
			return err
		}

		if options.HasKey {
			value, ok := secret.Values[options.Key]
			if !ok {
				return fmt.Errorf("No key '%s' in %s/%s", options.Key, expected, name)
			}
			// Raw and unwrapped so the value stays substitutable in shell.
			return writeValue(value)
		}
		if position > 0 {
			render.Raw("")
		}
		renderSecret(secret, namespace)
	}
	return nil
}

// decodeNamespace prints every Secret in the namespace, stacked.
//
// Confirms first unless --yes: unlike an indexed decode this prints every
// credential in the namespace, and it sits one flag away from the `kx secret`
// listing people run by reflex. Fetching before prompting costs nothing and
// discloses nothing, and lets the prompt name the blast radius.
func decodeNamespace(services Services, command SecretCommand, extra []string, yes bool) error {
	stop := render.Status("fetching secrets")
	secrets, err := command.ExecuteAll(extra)
	stop()
	if err != nil {
		return err
	}

	namespace := ""
	if len(secrets) > 0 {
		namespace = secrets[0].Namespace
	}
	if namespace == "" {
		if namespace = extractNamespace(extra); namespace == "" {
			namespace = services.Kubectl.CurrentNamespace()
		}
	}

	count := len(secrets)
	render.ScopeBanner(kinds.PluralDisplay("secret"), namespace, itemCount(count))
	if count == 0 {
		return nil
	}

	if !yes {
		noun := "Secrets"
		if count == 1 {
			noun = "Secret"
		}
		if err := services.confirm()(
			"Decode " + strconv.Itoa(count) + " " + noun + " in " + namespace + "?",
		); err != nil {
			return err
		}
	}
	for _, secret := range secrets {
		render.Raw("")
		// The namespace is left to the scope banner rather than repeated.
		renderSecret(secret, "")
	}
	return nil
}

// secretFlags pulls the flags `get` and `secret` share out of the raw
// arguments. See passthrough.go for why cobra can't do this.
func secretFlags(args []string) ([]string, getOptions, error) {
	options := getOptions{}
	match, rest, err := extractString(args, "--match", "-m")
	if err != nil {
		return nil, options, err
	}
	options.Match = match

	options.Decode, rest = extractBool(rest, "--decode")
	options.Yes, rest = extractBool(rest, "--yes", "-y")

	// Presence, not emptiness: `-k ""` asks for a key that cannot exist, and
	// must fail as a missing key rather than silently widening into a dump of
	// every value in the Secret.
	hasKey := hasFlag(rest, "--key", "-k")
	key, rest, err := extractString(rest, "--key", "-k")
	if err != nil {
		return nil, options, err
	}
	if hasKey {
		options.Key, options.HasKey = key, true
	}
	return rest, options, nil
}

func newSecretCommand(services Services, use string, aliases []string) *cobra.Command {
	cmd := &cobra.Command{
		Use:     use + " [index]... [kubectl flags]",
		Short:   "List Secrets like kx get, or show an indexed Secret's data with --decode; alias: kx secrets.",
		Aliases: aliases,
		Long:    "Lists Secrets exactly as kx get does. With --decode, prints an indexed Secret's data in plaintext, or every Secret in the namespace when no index is given.",
		Example: "  kx secret\n  kx secret 1 --decode\n  kx secret 1 --decode -k tls.crt\n  kx secret 1..3\n  kx secret 3..",
		// Everything not a kx flag belongs to kubectl.
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			rest, handled, err := passthrough(cmd, args, nil)
			if err != nil || handled {
				return err
			}
			rest, options, err := secretFlags(rest)
			if err != nil {
				return err
			}
			return runGet(services, "secret", rest, options)
		},
	}
	// Registered so they appear in the command's help; parsing is by hand.
	cmd.Flags().StringP("match", "m", "", "Match by name (substring, case-insensitive)")
	cmd.Flags().Bool("decode", false,
		"Show Secret data in plaintext; every Secret in the namespace when no index is given")
	cmd.Flags().StringP("key", "k", "", "With --decode, print only this key's value")
	cmd.Flags().BoolP("yes", "y", false,
		"Skip the confirmation prompt for a namespace-wide --decode")
	// Pure kubectl passthrough, parsed by hand like every other flag here —
	// registered only so they appear in --help instead of vanishing.
	cmd.Flags().StringP("namespace", "n", "", "Namespace to list from; defaults to the current namespace")
	cmd.Flags().BoolP("all-namespaces", "A", false, "List across every namespace; each row is indexed and carries its own namespace")
	registerWatchFlag(cmd)
	return cmd
}
