package cli

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/jzills/kx/internal/config"
	"github.com/jzills/kx/internal/events"
	"github.com/jzills/kx/internal/index"
	"github.com/jzills/kx/internal/kinds"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gopkg.in/yaml.v3"
)

// registerEvidenceTools adds the tools that read what happened to a resource
// — events and logs — rather than its current shape. Called from
// registerMCPTools.
func registerEvidenceTools(server *mcp.Server, deps mcpDeps) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "events",
		Description: "Warning and normal events recorded against exactly this object. Unlike logs, " +
			"a Deployment's events are its own, not its pods' — use tree to find the pods.",
		Annotations: readOnlyTool("Events"),
	}, serialized(deps, deps.events))
	mcp.AddTool(server, &mcp.Tool{
		Name: "logs",
		Description: "Recent container logs: one pod's, or every pod of a Deployment/StatefulSet/" +
			"DaemonSet/Service, prefixed by pod. Capped by tail (per pod) and 256 KiB.",
		Annotations: readOnlyTool("Logs"),
	}, serialized(deps, deps.logs))
}

const (
	defaultEventsLimit = 100
	maxEventsLimit     = 500
)

// staleTargetError turns a StaleResourceError into a sentence an MCP caller
// can act on. StaleResourceError.Error() is written for a CLI index or mark —
// with neither (the zero Ref this package always hands it) it drops the
// namespace entirely, which is the one fact worth keeping here.
func staleTargetError(err StaleResourceError) error {
	where := ""
	if err.Namespace != "" {
		where = " in " + err.Namespace
	}
	return fmt.Errorf("%s/%s no longer exists%s.", err.Kind, err.Name, where)
}

type eventsInput struct {
	Target mcpTarget `json:"target" jsonschema:"The resource whose events to show."`
	Since  string    `json:"since,omitempty" jsonschema:"Only events newer than this — 30m, 24h, 7d. Defaults to kx's events_max_age setting."`
	Limit  int       `json:"limit,omitempty" jsonschema:"Most events to return, newest first; default 100, at most 500."`
}

// mcpEvent is one event as a tool reports it.
type mcpEvent struct {
	Type    string `json:"type"`
	Reason  string `json:"reason"`
	Kind    string `json:"kind"`
	Message string `json:"message"`
	// At is RFC 3339, or absent when the cluster recorded no timestamp.
	At string `json:"at,omitempty"`
}

type eventsOutput struct {
	Context   string `json:"context"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
	Mark      string `json:"mark,omitempty"`
	Window    string `json:"window,omitempty"`
	Total     int    `json:"total"`
	Truncated int    `json:"truncated,omitempty"`
	// Events is never null, even when empty — an agent can range over it
	// without a nil check.
	Events []mcpEvent `json:"events"`
}

func (d mcpDeps) events(ctx context.Context, _ *mcp.CallToolRequest, in eventsInput) (*mcp.CallToolResult, eventsOutput, error) {
	window, err := resolveWindow(in.Since, d.Config.EventsMaxAge)
	if err != nil {
		return nil, eventsOutput{}, err
	}
	// The context before the client, as diagnose and tree read it.
	out := eventsOutput{Context: d.Kubectl.CurrentContext()}
	client, err := d.Kubernetes()
	if err != nil {
		return nil, eventsOutput{}, err
	}
	target, err := d.resolveTarget(in.Target)
	if err != nil {
		return nil, eventsOutput{}, err
	}
	out.Kind, out.Name, out.Namespace, out.Mark = string(target.Kind), target.Name, target.Namespace, target.Mark
	out.Window = windowLabel(window)

	command := EventsCommand{
		Kubectl: d.Kubectl,
		Events:  events.APIService{Client: client},
		Since:   events.Cutoff(window),
	}
	rows, err := command.ExecuteResource(ctx, target.Kind, target.Name, target.Namespace)
	if err != nil {
		var stale StaleResourceError
		if errors.As(err, &stale) {
			return nil, eventsOutput{}, staleTargetError(stale)
		}
		return nil, eventsOutput{}, err
	}

	sort.Slice(rows, func(i, j int) bool { return rows[i].Timestamp.After(rows[j].Timestamp) })
	out.Total = len(rows)
	limit := clampLimit(in.Limit, defaultEventsLimit, maxEventsLimit)
	kept := rows
	if len(rows) > limit {
		kept = rows[:limit]
		out.Truncated = out.Total - limit
	}
	out.Events = make([]mcpEvent, 0, len(kept))
	for _, row := range kept {
		out.Events = append(out.Events, mcpEvent{
			Type: row.Type, Reason: row.Reason, Kind: row.Kind, Message: row.Message, At: rfc3339(row.Timestamp),
		})
	}
	return nil, out, nil
}

const (
	defaultLogTail = 200
	maxLogTail     = 2000
	// maxLogBytes bounds the captured output kept in a tool result — 256 KiB.
	maxLogBytes = 256 * 1024
)

// boundLogBytes keeps the last maxLogBytes of output, cut at a line boundary
// so the first line kept is never a fragment. When that window holds no
// newline at all — one line longer than the whole cap — there is no line
// boundary to cut at, so it advances to the next UTF-8 rune boundary instead;
// the kept text still starts mid-line, but is always valid UTF-8.
func boundLogBytes(output string) (string, bool) {
	if len(output) <= maxLogBytes {
		return output, false
	}
	cut := output[len(output)-maxLogBytes:]
	if idx := strings.IndexByte(cut, '\n'); idx >= 0 {
		cut = cut[idx+1:]
	} else {
		for len(cut) > 0 && !utf8.RuneStart(cut[0]) {
			cut = cut[1:]
		}
	}
	return cut, true
}

// countLines counts the lines in text, the way a terminal would show them: a
// trailing newline does not count as one more empty line.
func countLines(text string) int {
	if text == "" {
		return 0
	}
	lines := strings.Count(text, "\n")
	if !strings.HasSuffix(text, "\n") {
		lines++
	}
	return lines
}

type logsInput struct {
	Target    mcpTarget `json:"target" jsonschema:"The Pod, or workload whose pods to aggregate, to show logs for."`
	Container string    `json:"container,omitempty" jsonschema:"Container name, for a Pod with more than one. Not valid with an aggregate target — pick one pod instead."`
	Since     string    `json:"since,omitempty" jsonschema:"Only log lines from this far back — 30m, 24h, 7d."`
	Tail      int       `json:"tail,omitempty" jsonschema:"Lines to keep per pod, most recent first; default 200, at most 2000."`
	Previous  bool      `json:"previous,omitempty" jsonschema:"Show the previous (crashed) container's logs instead of the current one's."`
}

type logsOutput struct {
	Context   string `json:"context"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
	Mark      string `json:"mark,omitempty"`
	Lines     int    `json:"lines"`
	Truncated bool   `json:"truncated,omitempty"`
	Logs      string `json:"logs"`
}

func (d mcpDeps) logs(_ context.Context, _ *mcp.CallToolRequest, in logsInput) (*mcp.CallToolResult, logsOutput, error) {
	if in.Container != "" {
		if err := validObjectName(in.Container); err != nil {
			return nil, logsOutput{}, err
		}
	}
	var sinceArg string
	if in.Since != "" {
		window, err := config.ParseDuration(in.Since)
		if err != nil {
			return nil, logsOutput{}, fmt.Errorf("'--since': %w", err)
		}
		sinceArg = window.String()
	}

	out := logsOutput{Context: d.Kubectl.CurrentContext()}
	target, err := d.resolveTarget(in.Target)
	if err != nil {
		return nil, logsOutput{}, err
	}
	out.Kind, out.Name, out.Namespace, out.Mark = string(target.Kind), target.Name, target.Namespace, target.Mark
	tail := clampLimit(in.Tail, defaultLogTail, maxLogTail)

	var args []string
	switch {
	case target.Kind == kinds.Pod:
		args = []string{"logs", target.Name, "-n", target.Namespace, fmt.Sprintf("--tail=%d", tail)}
		if sinceArg != "" {
			args = append(args, "--since="+sinceArg)
		}
		if in.Container != "" {
			args = append(args, "-c", in.Container)
		}
		if in.Previous {
			args = append(args, "--previous")
		}
	case aggregateLogKinds.Has(target.Kind):
		if in.Container != "" {
			return nil, logsOutput{}, errors.New(
				"'container' applies to a Pod target — pick one of the workload's pods with tree.")
		}
		selector, err := (LogsCommand{Kubectl: d.Kubectl, Status: silentStatus}).
			selector(target.Name, target.Namespace, target.Kind)
		if err != nil {
			return nil, logsOutput{}, err
		}
		args = []string{"logs", "-l", selector, "--prefix=true", "-n", target.Namespace, fmt.Sprintf("--tail=%d", tail)}
		if sinceArg != "" {
			args = append(args, "--since="+sinceArg)
		}
		if in.Previous {
			args = append(args, "--previous")
		}
	default:
		return nil, logsOutput{}, unsupportedKindError("logs", target.Kind, logKinds)
	}

	output, err := d.Kubectl.Run(args)
	if err != nil {
		return nil, logsOutput{}, err
	}
	text, truncated := boundLogBytes(output)
	out.Lines = countLines(text)
	out.Truncated = truncated
	out.Logs = text
	return nil, out, nil
}

const (
	defaultTopLimit = 200
	maxTopLimit     = 1000
)

type topInput struct {
	Namespace     string `json:"namespace,omitempty" jsonschema:"Namespace to read; defaults to the current namespace. Not valid with nodes."`
	AllNamespaces bool   `json:"allNamespaces,omitempty" jsonschema:"Read across every namespace. Not valid with nodes."`
	Nodes         bool   `json:"nodes,omitempty" jsonschema:"Report node usage against capacity instead of pod usage against limits."`
	Limit         int    `json:"limit,omitempty" jsonschema:"Most rows to return; default 200, at most 1000."`
}

type topOutput struct {
	Context string      `json:"context"`
	Top     topDocument `json:"top"`
}

func (d mcpDeps) top(_ context.Context, _ *mcp.CallToolRequest, in topInput) (*mcp.CallToolResult, topOutput, error) {
	if in.Namespace != "" {
		if err := validNamespace(in.Namespace); err != nil {
			return nil, topOutput{}, err
		}
	}
	if err := scopeConflict(in.Namespace, in.AllNamespaces); err != nil {
		return nil, topOutput{}, err
	}
	if in.Nodes {
		if in.Namespace != "" {
			return nil, topOutput{}, clusterScopedScopeError("namespace", "nodes")
		}
		if in.AllNamespaces {
			return nil, topOutput{}, clusterScopedScopeError("allNamespaces", "nodes")
		}
	}

	out := topOutput{Context: d.Kubectl.CurrentContext()}
	command := TopCommand{Kubectl: d.Kubectl, State: discardWriter{}, Index: index.Service{}}

	var indexed index.Table
	var err error
	resource := "pods"
	var subject scanSubject
	switch {
	case in.Nodes:
		resource = "nodes"
		indexed, _, err = command.ExecuteNodes("", nil)
	case in.AllNamespaces:
		indexed, _, err = command.Execute("", []string{"-A"}, false)
		subject.AllNamespaces = true
	default:
		namespace := in.Namespace
		if namespace == "" {
			namespace = d.Kubectl.CurrentNamespace()
		}
		indexed, _, err = command.Execute("", []string{"-n", namespace}, false)
		subject.Namespace = namespace
	}
	if err != nil {
		return nil, topOutput{}, err
	}

	rows := topPageRows(indexed)
	// No numeric indexes in a tool result — see resolveTarget and the package
	// doc comment in mcp.go. topPageRows reads the "X" column TopCommand just
	// indexed, so every row's Index is zeroed before it goes near the document.
	for i := range rows {
		rows[i].Index = 0
	}
	total := len(rows)
	limit := clampLimit(in.Limit, defaultTopLimit, maxTopLimit)
	truncated := 0
	if total > limit {
		rows = rows[:limit]
		truncated = total - limit
	}
	out.Top = topDocumentOf(subject, resource, rows)
	out.Top.Truncated = truncated
	return nil, out, nil
}

// fieldNamePattern is the shape a get_yaml field must have. Letters and
// digits only, starting with a letter — findKeys matches a field against a
// manifest's own map keys, which are YAML/JSON identifiers, never a kubectl
// flag or a path expression, so there is no '-', '.' or '/' to allow.
var fieldNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*$`)

// maxYAMLBytes bounds an encoded manifest kept in a tool result — 256 KiB,
// matching logs' own bound (boundLogBytes). Unlike logs, a manifest is a
// single document that cannot be cut mid-stream without producing invalid
// YAML, so an oversized one is refused rather than truncated.
const maxYAMLBytes = 256 * 1024

// redactSecretAnnotation is the annotation kubectl apply stamps with the
// whole manifest it last applied — a Secret's plaintext data included — so
// get_yaml must redact it exactly as it redacts data and stringData.
const redactSecretAnnotation = "kubectl.kubernetes.io/last-applied-configuration"

// isCoreSecret reports whether a decoded manifest is a core/v1 Secret. A kind
// named Secret in any other API group is not one.
func isCoreSecret(document any) bool {
	root, ok := document.(map[string]any)
	return ok && root["kind"] == string(kinds.Secret) && root["apiVersion"] == "v1"
}

// redactSecret masks a Secret manifest's plaintext values: every value under
// top-level data and stringData becomes "<redacted>" with its key kept, and
// the last-applied-configuration annotation — which carries the whole
// manifest, Secret data included — is redacted the same way.
//
// A pure function of the decoded document, so it has its own table test
// independent of the tool plumbing around it. Anything other than a
// map[string]any (a malformed or empty document) is returned unchanged: there
// is nothing shaped like a Secret to redact.
func redactSecret(document any) any {
	root, ok := document.(map[string]any)
	if !ok {
		return document
	}
	redacted := make(map[string]any, len(root))
	for key, value := range root {
		redacted[key] = value
	}
	for _, key := range []string{"data", "stringData"} {
		values, ok := redacted[key].(map[string]any)
		if !ok {
			continue
		}
		masked := make(map[string]any, len(values))
		for field := range values {
			masked[field] = "<redacted>"
		}
		redacted[key] = masked
	}
	if metadata, ok := redacted["metadata"].(map[string]any); ok {
		if annotations, ok := metadata["annotations"].(map[string]any); ok {
			if _, present := annotations[redactSecretAnnotation]; present {
				maskedAnnotations := make(map[string]any, len(annotations))
				for key, value := range annotations {
					maskedAnnotations[key] = value
				}
				maskedAnnotations[redactSecretAnnotation] = "<redacted>"
				maskedMetadata := make(map[string]any, len(metadata))
				for key, value := range metadata {
					maskedMetadata[key] = value
				}
				maskedMetadata["annotations"] = maskedAnnotations
				redacted["metadata"] = maskedMetadata
			}
		}
	}
	return redacted
}

type getYamlInput struct {
	Target mcpTarget `json:"target" jsonschema:"The resource whose manifest to show."`
	Fields []string  `json:"fields,omitempty" jsonschema:"Only these top-level or nested keys, shallowest match first — e.g. [\"spec\", \"status\"]."`
}

type yamlOutput struct {
	Context   string `json:"context"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
	Mark      string `json:"mark,omitempty"`
	Redacted  bool   `json:"redacted,omitempty"`
	YAML      string `json:"yaml"`
}

func (d mcpDeps) getYAML(_ context.Context, _ *mcp.CallToolRequest, in getYamlInput) (*mcp.CallToolResult, yamlOutput, error) {
	for _, field := range in.Fields {
		if !fieldNamePattern.MatchString(field) {
			return nil, yamlOutput{}, fmt.Errorf(
				"field '%s' is not a manifest key — use letters and digits only, e.g. \"spec\".", field)
		}
	}

	out := yamlOutput{Context: d.Kubectl.CurrentContext()}
	target, err := d.resolveTarget(in.Target)
	if err != nil {
		return nil, yamlOutput{}, err
	}
	out.Kind, out.Name, out.Namespace, out.Mark = string(target.Kind), target.Name, target.Namespace, target.Mark

	raw, err := d.Kubectl.Run(target.getArgs("-o", "yaml"))
	if err != nil {
		return nil, yamlOutput{}, err
	}

	var document any
	if err := yaml.Unmarshal([]byte(raw), &document); err != nil {
		return nil, yamlOutput{}, err
	}
	// Redacted on the resolved kind, not the caller's own spelling of it — a
	// mark that aliases a Secret must be redacted exactly as naming the
	// Secret directly would be — and on what came back as well: kubectl
	// accepts spellings of a Secret that no kind check can list in full, so a
	// document that is a core/v1 Secret is redacted however it was asked for.
	if target.Kind == kinds.Secret || isCoreSecret(document) {
		document = redactSecret(document)
		out.Redacted = true
	}
	if len(in.Fields) > 0 {
		document = findKeys(document, in.Fields)
	}

	encoded, err := encodeYAML(document)
	if err != nil {
		return nil, yamlOutput{}, err
	}
	if len(encoded) > maxYAMLBytes {
		return nil, yamlOutput{}, fmt.Errorf(
			"The manifest is %d KiB, over the 256 KiB limit — pass fields to narrow it, e.g. [\"spec\"].",
			(len(encoded)+1023)/1024)
	}
	out.YAML = encoded
	return nil, out, nil
}
