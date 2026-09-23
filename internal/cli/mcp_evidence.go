package cli

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/jzills/kx/internal/config"
	"github.com/jzills/kx/internal/events"
	"github.com/jzills/kx/internal/kinds"
	"github.com/modelcontextprotocol/go-sdk/mcp"
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
		selector, err := (LogsCommand{Kubectl: d.Kubectl, Status: func(string) func() { return func() {} }}).
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
