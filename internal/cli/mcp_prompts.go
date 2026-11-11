package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The prompts: investigations written out once, which a client offers as
// commands (Claude Code lists each as /kx:<name>). A prompt returns text and
// nothing else. It reads no state, reaches no cluster and writes nothing;
// the agent does the work afterwards with the tools, under every rule the
// tools already keep. So the prompts are not serialized: there is nothing
// in them for a concurrent call to race.
//
// Arguments arrive as single words, since Claude Code splits what follows the
// command on whitespace. A target is one of the three spellings the CLI
// takes: kind/name, @mark, or an index.

func registerMCPPrompts(server *mcp.Server, deps mcpDeps) {
	namespace := &mcp.PromptArgument{
		Name:        "namespace",
		Description: "The namespace to look in; defaults to the kubeconfig's current one.",
	}
	target := &mcp.PromptArgument{
		Name:        "target",
		Description: "The resource: kind/name such as deploy/api, a mark such as @api, or an index from your current kx listing.",
		Required:    true,
	}
	targetNamespace := &mcp.PromptArgument{
		Name:        "namespace",
		Description: "The namespace of a kind/name target; defaults to the kubeconfig's current one.",
	}

	server.AddPrompt(&mcp.Prompt{
		Name:  "triage",
		Title: "Triage a namespace",
		Description: "Find what is unhealthy in a namespace, follow the worst of it to a cause, " +
			"and mark the resource at fault.",
		Arguments: []*mcp.PromptArgument{namespace},
	}, deps.triagePrompt)

	server.AddPrompt(&mcp.Prompt{
		Name:        "explain",
		Title:       "Explain one resource",
		Description: "Find out why one resource is unhealthy, from its findings, ownership, events and logs.",
		Arguments:   []*mcp.PromptArgument{target, targetNamespace},
	}, deps.explainPrompt)

	server.AddPrompt(&mcp.Prompt{
		Name:  "rollout",
		Title: "Check the latest rollout",
		Description: "Find out whether a workload's latest rollout broke it, by comparing its last two " +
			"revisions against what has gone wrong since.",
		Arguments: []*mcp.PromptArgument{target, targetNamespace},
	}, deps.rolloutPrompt)

	server.AddPrompt(&mcp.Prompt{
		Name:        "cves",
		Title:       "Summarize image CVEs",
		Description: "Scan a namespace's container images and summarize what is fixable, worst image first.",
		Arguments:   []*mcp.PromptArgument{namespace},
	}, deps.cvesPrompt)
}

// promptResult is one user message holding text.
func promptResult(description, text string) *mcp.GetPromptResult {
	return &mcp.GetPromptResult{
		Description: description,
		Messages:    []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: text}}},
	}
}

// promptWord reads one argument, refusing anything that is not a single
// word: it is written into the prompt's text, and a newline in it would
// start an instruction the user never typed.
func promptWord(req *mcp.GetPromptRequest, name string) (string, error) {
	value := strings.TrimSpace(req.Params.Arguments[name])
	if strings.IndexFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return "", fmt.Errorf("The %s argument is one word — %q has spaces in it.", name, value)
	}
	return value, nil
}

// promptScope is the sentence a namespace argument becomes, with the tool
// arguments that carry it.
func promptScope(req *mcp.GetPromptRequest) (sentence, arguments string, err error) {
	namespace, err := promptWord(req, "namespace")
	if err != nil {
		return "", "", err
	}
	if namespace == "" {
		return "the kubeconfig's current namespace", "no namespace argument", nil
	}
	return "namespace " + namespace, fmt.Sprintf(`namespace %q`, namespace), nil
}

// promptTarget turns a target word into the target argument the tools take,
// as JSON, and says whether it is an index, which the agent has to confirm
// back by name before it acts on it.
func promptTarget(req *mcp.GetPromptRequest) (target string, index bool, err error) {
	word, err := promptWord(req, "target")
	if err != nil {
		return "", false, err
	}
	namespace, err := promptWord(req, "namespace")
	if err != nil {
		return "", false, err
	}
	var value mcpTarget
	switch {
	case word == "":
		return "", false, errors.New("A target is needed — give kind/name such as deploy/api, a mark such as @api, or an index.")
	case strings.HasPrefix(word, "@"):
		value.Mark = word
	case strings.Contains(word, "/"):
		kind, name, _ := strings.Cut(word, "/")
		if kind == "" || name == "" || strings.Contains(name, "/") {
			return "", false, fmt.Errorf("%q is not kind/name — give one such as deploy/api.", word)
		}
		value.Kind, value.Name, value.Namespace = kind, name, namespace
	default:
		number, convErr := strconv.Atoi(word)
		if convErr != nil || number < 1 {
			return "", false, fmt.Errorf("%q is not a target — give kind/name such as deploy/api, a mark such as @api, or an index.", word)
		}
		value.Index, index = number, true
	}
	// The same refusal the CLI makes for -n beside an index: a mark or an
	// index already carries its namespace.
	if namespace != "" && value.Kind == "" {
		return "", false, errors.New("A namespace goes with a kind/name target only — a mark or an index already carries its own.")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", false, err
	}
	return string(encoded), index, nil
}

// promptLabel is the target as the user typed it, for the description a
// client shows beside the prompt's text.
func promptLabel(req *mcp.GetPromptRequest) string {
	return strings.TrimSpace(req.Params.Arguments["target"])
}

// confirmIndex is the step an index target adds: say which resource it
// resolved to before going further, as the server's instructions ask.
func confirmIndex(index bool) string {
	if !index {
		return ""
	}
	return "The target is a row of my current kx listing. Before anything else, tell me which " +
		"resource it resolved to, by kind and name.\n\n"
}

func (d mcpDeps) triagePrompt(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	scope, arguments, err := promptScope(req)
	if err != nil {
		return nil, err
	}
	return promptResult("Triage "+scope, fmt.Sprintf(
		"Triage %s with the kx tools.\n\n"+
			"1. Call diagnose with no target and %s, to sweep it. If nothing is unhealthy, say so "+
			"and stop.\n"+
			"2. Take the worst finding. Use tree to find the Pods it comes down to, then events and "+
			"logs on the right one, until you can name a cause. A finding that only follows from "+
			"another (a Deployment short of replicas because its Pods crash) is not the cause.\n"+
			"3. Mark the resource at fault with the mark tool, under a short name that list_marks "+
			"does not already show, so I can reach it in kx as @name.\n"+
			"4. Report the cause in a sentence or two, the evidence for it, the mark, and anything "+
			"else the sweep found that looks unrelated. Suggest a fix, but change nothing: the kx "+
			"tools are read-only.",
		scope, arguments)), nil
}

func (d mcpDeps) explainPrompt(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	target, index, err := promptTarget(req)
	if err != nil {
		return nil, err
	}
	return promptResult("Explain "+promptLabel(req), fmt.Sprintf(
		"Find out why this resource is unhealthy, with the kx tools. Pass it to each tool as the "+
			"target %s.\n\n%s"+
			"1. Call diagnose on it. If it is healthy, say so and stop.\n"+
			"2. Call tree on it to see what it owns, and follow the findings down to the Pod or "+
			"container they come from.\n"+
			"3. Read that resource's events, then its logs, previous ones included when a "+
			"container has restarted.\n"+
			"4. Report the cause in a sentence or two and the evidence for it, quoting the event "+
			"or log line that shows it. Suggest a fix, but change nothing: the kx tools are "+
			"read-only.",
		target, confirmIndex(index))), nil
}

func (d mcpDeps) rolloutPrompt(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	target, index, err := promptTarget(req)
	if err != nil {
		return nil, err
	}
	return promptResult("Check the latest rollout of "+promptLabel(req), fmt.Sprintf(
		"Find out whether this workload's latest rollout broke it, with the kx tools. Pass it to "+
			"each tool as the target %s.\n\n%s"+
			"1. Call rollout_history on it to list its revisions. If it has only one, say there is "+
			"nothing to compare and go on to step 3.\n"+
			"2. Call rollout_history again with the latest revision, and with the one before it, "+
			"and compare their Pod templates: images, environment, resources, probes, commands.\n"+
			"3. Call diagnose and events on it, and note when each problem started against when "+
			"the latest revision rolled out.\n"+
			"4. Say whether the rollout looks like the cause: which change, and the evidence that "+
			"ties it to the failure, or why the timing rules it out. If rolling back looks right, "+
			"say so, but change nothing: the kx tools are read-only.",
		target, confirmIndex(index))), nil
}

func (d mcpDeps) cvesPrompt(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	scope, arguments, err := promptScope(req)
	if err != nil {
		return nil, err
	}
	return promptResult("Summarize image CVEs in "+scope, fmt.Sprintf(
		"Summarize the known CVEs in the container images of %s, with the kx tools.\n\n"+
			"1. Call scan with no target and %s. A sweep can take minutes; wait for it.\n"+
			"2. Rank the images by their critical and high findings that have a fix available, "+
			"worst first. Findings with no fix go in a line of their own per image, not in the "+
			"ranking.\n"+
			"3. For each of the worst few images, list the packages to upgrade, from the "+
			"installed version to the one that fixes it.\n"+
			"4. Keep it short: a table of images, then the upgrades. An image the scan could "+
			"not read is reported with its error, not left out.",
		scope, arguments)), nil
}
