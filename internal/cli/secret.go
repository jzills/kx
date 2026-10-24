package cli

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"unicode/utf8"

	"github.com/jzills/kx/internal/index"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/kubectl"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/state"
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
			return nil, nil, fmt.Errorf("Key '%s' is not valid base64: %w.", key, err)
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
func (c SecretCommand) Execute(ref state.Ref) (secretData, error) {
	name, namespace, kind, err := c.State.Resolve(ref)
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
//
// resolved has already been resolved — and, for a non-empty batch, kind
// checked against kinds.Secret — by the caller's resolveParsedExpecting call
// before decodeSecrets is ever reached, so every entry here is safe to fetch.
// That is what keeps `kx get secrets --decode 1 99` from decoding and
// printing index 1's plaintext before failing on the out-of-range 99: the
// whole batch is validated before any of it is acted on, rather than
// resolving and fetching one index at a time.
func decodeSecrets(services Services, resource string, resolved []Resolved, extra []string, options getOptions) error {
	if !options.Decode {
		return fmt.Errorf("'--key' requires '--decode' — add it, or drop '--key'.")
	}
	expected := kinds.Normalize(resource)
	if expected != kinds.Secret {
		return fmt.Errorf(
			"'--decode' cannot be combined with %s — only Secrets carry data to decode.",
			kinds.PluralDisplay(string(expected)))
	}
	if options.HasKey && len(resolved) != 1 {
		return fmt.Errorf("'--key' takes a single index — give one, or drop '--key' to decode each Secret whole.")
	}

	command := SecretCommand{Kubectl: services.Kubectl, State: services.State}
	if len(resolved) == 0 {
		return decodeNamespace(services, command, extra, options.Match, options.Yes)
	}
	// Refused rather than ignored: the index already names the Secret, and a
	// term that narrowed nothing on a command printing credentials would look
	// as though it had.
	if options.Match != "" {
		return errMatchBesideIndex
	}

	for position, target := range resolved {
		stop := render.Status("fetching Secret")
		secret, err := command.Execute(target.Ref)
		stop()
		if err != nil {
			// A NotFound here means the saved index outlived the Secret; the
			// explicit type triggers the refresh path.
			if IsNotFound(err) {
				return StaleResourceError{
					Kind: expected, Name: target.Name, Namespace: target.Namespace, Ref: target.Ref,
				}
			}
			return err
		}

		if options.HasKey {
			value, ok := secret.Values[options.Key]
			if !ok {
				return fmt.Errorf("No key '%s' in %s/%s.", options.Key, expected, target.Name)
			}
			// Raw and unwrapped so the value stays substitutable in shell.
			return writeValue(value)
		}
		if position > 0 {
			render.Raw("")
		}
		renderSecret(secret, target.Namespace)
	}
	return nil
}

// decodeNamespace prints every Secret in the namespace whose name contains
// match, stacked — every Secret when match is empty.
//
// Confirms first unless --yes: unlike an indexed decode this prints every
// credential in the namespace, and it sits one flag away from the `kx secret`
// listing people run by reflex. Fetching before prompting costs nothing and
// discloses nothing, and lets the prompt name the blast radius.
func decodeNamespace(services Services, command SecretCommand, extra []string, match string, yes bool) error {
	stop := render.Status("fetching Secrets")
	secrets, err := command.ExecuteAll(extra)
	stop()
	if err != nil {
		return err
	}

	// The scope the banner and the prompt name is the reach of the decode.
	// Under -A that is every namespace, and each Secret carries its own: read
	// off the first one, "Decode 9 Secrets in diagnostics?" guarded Secrets
	// from three.
	spanning := allNamespaces(extra)
	namespace := render.AllNamespaces
	if !spanning {
		namespace = ""
		if len(secrets) > 0 {
			namespace = secrets[0].Namespace
		}
		if namespace == "" {
			if namespace = extractNamespace(extra); namespace == "" {
				namespace = services.Kubectl.CurrentNamespace()
			}
		}
	}

	// Narrowed before anything is counted or printed, as the listing is: the
	// term is how a decode leaves credentials out, so the prompt must count
	// only what it will print.
	matches := index.NameMatcher(match)
	kept := secrets[:0:0]
	for _, secret := range secrets {
		if matches(secret.Name) {
			kept = append(kept, secret)
		}
	}
	secrets = kept

	count := len(secrets)
	// A term that matched nothing keeps the count and names the term in a
	// row beneath, as kx get secrets -m does. See render.EmptyMatch.
	if count == 0 && match != "" {
		render.EmptyMatch(kinds.PluralDisplay("secret"), namespace, itemCount(0), match)
	} else {
		render.ScopeBanner(kinds.PluralDisplay("secret"), namespace, itemCount(count))
	}
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
		// The namespace is left to the scope banner rather than repeated,
		// unless the banner spans them.
		where := ""
		if spanning {
			where = secret.Namespace
		}
		renderSecret(secret, where)
	}
	return nil
}

func newSecretCommand(services Services, use string, aliases []string) *cobra.Command {
	cmd := &cobra.Command{
		Use:     use + " [index]... [kubectl flags]",
		Short:   "List Secrets like kx get, or show an indexed Secret's data with --decode; alias: kx secrets.",
		Aliases: aliases,
		Long: "Lists Secrets exactly as kx get does. With `--decode`, prints an indexed Secret's data in plaintext, " +
			"or every Secret in the namespace when no index is given.\n\n" + secretSortLong,
		Example: "  kx secret\n  kx secret 1 --decode\n  kx secret 1 --decode -k tls.crt\n  kx secret 1..3\n  kx secret 3..\n  kx secret -l app=web\n  kx secret --sort type,-age",
	}
	// kubectl get's flags, as kx get takes them: kx secret is kubectl get
	// secret.
	kubectlFlags := registerGetFlags(cmd, secretSortUsage)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		positional, forward := kubectlArgv(cmd, args, kubectlFlags)
		options, err := getOptionsFrom(cmd)
		if err != nil {
			return err
		}
		// The indexes lead, as runGet reads them, then kubectl's flags; what
		// followed `--` is handed over apart, so none of it is an index.
		options.AfterDash = afterDash(cmd, args)
		flags := forward[:len(forward)-len(options.AfterDash)]
		rest := append(positional[:len(positional):len(positional)], flags...)
		return runGet(services, "secret", rest, options)
	}
	return cmd
}
