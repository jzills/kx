package cli

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/jzills/kx/internal/scanner"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerScanTool adds scan, the one tool not registered through serialized.
//
// A namespace sweep can run for minutes, and serialized would hold the server
// lock for all of it, stalling every other call. The lock guards the state
// file, the discovery source and client-go; resolving images touches those
// (a mark, a kind's shorthand, kubectl reading the current namespace), but
// running the scanner binaries touches none of them. So the handler takes the
// lock through d.locked for the resolve phase only, and scans outside it.
func registerScanTool(server *mcp.Server, deps mcpDeps) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "scan",
		Description: "Scan container images for known CVEs with the configured engine (grype, trivy or " +
			"scout). A namespace sweep can take minutes; progress is reported when the client asks for it. " +
			"Counts cover every severity; findings list the worst first. At most imageLimit images are scanned, " +
			"in the order they were found; truncatedImages counts the rest.",
		// Not idempotent: a scanner pulls fresh vulnerability data, so the
		// same image can report differently from one call to the next. Open
		// world because it reaches registries and vulnerability databases.
		Annotations: &mcp.ToolAnnotations{Title: "Scan images", ReadOnlyHint: true, OpenWorldHint: boolPtr(true)},
	}, deps.scan)
}

const (
	defaultScanFindingsLimit = 20
	maxScanFindingsLimit     = 200
	defaultMinSeverity       = "high"
	// A sweep's images are bounded as well as each image's findings, so a
	// cluster-wide sweep cannot run for hours or return an unbounded result.
	defaultScanImageLimit = 50
	maxScanImageLimit     = 200
)

type scanInput struct {
	Target        *mcpTarget `json:"target,omitempty" jsonschema:"The workload whose images to scan: a Pod, Deployment, ReplicaSet, StatefulSet, DaemonSet, Job or CronJob. Omit to sweep a namespace."`
	Namespace     string     `json:"namespace,omitempty" jsonschema:"Namespace to sweep when there is no target; defaults to the current namespace."`
	AllNamespaces bool       `json:"allNamespaces,omitempty" jsonschema:"Sweep every namespace when there is no target."`
	Engine        string     `json:"engine,omitempty" jsonschema:"Scanner to use: grype, trivy or scout. Defaults to kx's configured engine."`
	MinSeverity   string     `json:"minSeverity,omitempty" jsonschema:"Least severe finding to list: critical, high, medium or low; default high. Counts always cover every severity."`
	Limit         int        `json:"limit,omitempty" jsonschema:"Most findings to list per image, worst first; default 20, at most 200. Each image's truncated counts the findings at minSeverity or worse that were left out."`
	ImageLimit    int        `json:"imageLimit,omitempty" jsonschema:"Most images to scan, in the order they were found; default 50, at most 200. truncatedImages counts the images left unscanned."`
}

type scanOutput struct {
	Context string `json:"context"`
	// Mark is the mark the target was given as, echoed the way every other
	// target-taking tool echoes it.
	Mark string       `json:"mark,omitempty"`
	Scan scanDocument `json:"scan"`
	// TruncatedImages counts the images found but not scanned because of
	// imageLimit.
	TruncatedImages int `json:"truncatedImages,omitempty"`
}

// severityRank is a document severity's position in scanner.Severities, most
// severe first; an unknown spelling ranks below every known one.
func severityRank(token string) int {
	for position, severity := range scanner.Severities {
		if severityToken(severity) == token {
			return position
		}
	}
	return len(scanner.Severities)
}

// parseMinSeverity reads minSeverity into a rank. UNSPECIFIED is a bucket, not
// a level, so it is refused here as --fail-on refuses it.
func parseMinSeverity(value string) (int, error) {
	if value == "" {
		value = defaultMinSeverity
	}
	token := strings.ToLower(value)
	rank := severityRank(token)
	if rank >= len(scanner.Severities) || token == "unspecified" {
		return 0, fmt.Errorf(
			"Invalid value for 'minSeverity': '%s'. Accepted values: critical, high, medium, low.", value)
	}
	return rank, nil
}

// listFindings keeps an image's findings at minRank or worse, worst first —
// stable, so the scanner's own order holds within a severity — and at most
// limit of them, recording how many the limit cut. Counts are left whole.
func listFindings(image *jsonImage, minRank, limit int) {
	kept := image.Findings[:0]
	for _, finding := range image.Findings {
		if severityRank(finding.Severity) <= minRank {
			kept = append(kept, finding)
		}
	}
	sort.SliceStable(kept, func(i, j int) bool {
		return severityRank(kept[i].Severity) < severityRank(kept[j].Severity)
	})
	if len(kept) > limit {
		image.Truncated = len(kept) - limit
		kept = kept[:limit]
	}
	image.Findings = kept
}

func (d mcpDeps) scan(ctx context.Context, req *mcp.CallToolRequest, in scanInput) (*mcp.CallToolResult, scanOutput, error) {
	var (
		out     scanOutput
		engine  string
		images  []string
		subject scanSubject
		minRank int
	)
	command := ScanCommand{Kubectl: d.Kubectl, Scanner: d.Scanner, Status: silentStatus}

	// The resolve phase, under the server lock: everything that reads the
	// state file, the discovery source or the cluster.
	err := d.locked(func() error {
		if in.Namespace != "" {
			if err := validNamespace(in.Namespace); err != nil {
				return err
			}
		}
		if err := scopeConflict(in.Namespace, in.AllNamespaces); err != nil {
			return err
		}
		if in.Target != nil && (in.Namespace != "" || in.AllNamespaces) {
			return errors.New(
				"'namespace' and 'allNamespaces' apply without a target — a target already names its namespace.")
		}
		var err error
		if minRank, err = parseMinSeverity(in.MinSeverity); err != nil {
			return err
		}
		// The name only selects an engine; it never reaches a command line.
		// ExecuteResource and Collect both start with EnsureAvailable, whose
		// scanner.GetEngine refuses a name that is not one of kx's own — with
		// its own sentence — before the cluster or any scanner is touched.
		engine = in.Engine
		if engine == "" {
			engine = d.Config.Engine
		}

		out.Context = d.Kubectl.CurrentContext()
		if in.Target != nil {
			target, err := d.resolveTarget(*in.Target)
			if err != nil {
				return err
			}
			// ExecuteResource refuses a kind with no pod template, then checks
			// the engine is installed before reading the cluster.
			images, err = command.ExecuteResource(target.Kind, target.Name, target.Namespace, engine)
			subject = scanSubject{Kind: target.Kind, Name: target.Name, Namespace: target.Namespace}
			out.Mark = target.Mark
			return err
		}
		scope := scanScope{Namespace: in.Namespace, All: in.AllNamespaces}
		if !scope.All && scope.Namespace == "" {
			scope.Namespace = d.Kubectl.CurrentNamespace()
		}
		images, err = command.Collect(scope, engine)
		subject = scanSubject{Namespace: scope.Namespace, AllNamespaces: scope.All}
		return err
	})
	if err != nil {
		return nil, scanOutput{}, err
	}

	if imageLimit := clampLimit(in.ImageLimit, defaultScanImageLimit, maxScanImageLimit); len(images) > imageLimit {
		out.TruncatedImages = len(images) - imageLimit
		images = images[:imageLimit]
	}

	// The scan phase, outside the lock: only the scanner binaries run here,
	// one scan's worth at a time. A caller that gives up while queued leaves
	// the queue, and one that gives up mid-scan stops further images.
	select {
	case d.scanSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, scanOutput{}, ctx.Err()
	}
	defer func() { <-d.scanSlots }()
	rows, err := command.SummarizeContext(ctx, engine, images, scanProgress(ctx, req, len(images)))
	if err != nil {
		return nil, scanOutput{}, err
	}
	out.Scan = scanDocumentOf(subject, rows)
	limit := clampLimit(in.Limit, defaultScanFindingsLimit, maxScanFindingsLimit)
	for i := range out.Scan.Images {
		listFindings(&out.Scan.Images[i], minRank, limit)
	}
	return nil, out, nil
}

// scanProgress is Summarize's onScanned for one request: an MCP progress
// notification per image, or nil when the client sent no progress token and
// so asked for none.
//
// Summarize calls it from its worker goroutines. The count and the send share
// one mutex rather than the count alone being atomic: with two workers, an
// atomic count could be sent as 2 before 1, and progress must only ever rise.
// A failed notification is dropped — progress is a courtesy, and the scan's
// result still reaches the client.
func scanProgress(ctx context.Context, req *mcp.CallToolRequest, total int) func() {
	if req == nil || req.Session == nil || req.Params == nil {
		return nil
	}
	token := req.Params.GetProgressToken()
	if token == nil {
		return nil
	}
	var (
		mu   sync.Mutex
		done int
	)
	return func() {
		mu.Lock()
		defer mu.Unlock()
		done++
		_ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
			ProgressToken: token,
			Progress:      float64(done),
			Total:         float64(total),
			Message:       fmt.Sprintf("scanned %d of %d images", done, total),
		})
	}
}
