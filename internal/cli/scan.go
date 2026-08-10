package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/kubectl"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/scanner"
	"github.com/jzills/kx/internal/web"
	"github.com/spf13/cobra"
)

var scannableKinds = map[kinds.Kind]bool{
	kinds.Pod: true, kinds.Deployment: true, kinds.ReplicaSet: true,
	kinds.StatefulSet: true, kinds.DaemonSet: true, kinds.Job: true,
	kinds.CronJob: true,
}

// namespaceScanKinds are the workload kinds swept for a namespace-level scan,
// as a single kubectl selector.
const namespaceScanKinds = "deployments,statefulsets,daemonsets,cronjobs,jobs,pods"

// scanScope is the namespace selection for a sweep: one namespace, or all of
// them. The kubectl selector, the banner label and the empty-result message
// all have to agree about the scope, so they live together rather than being
// rebuilt at each call site.
//
// An empty Namespace does not mean "all" — client-go spells it that way and
// diag's Sweep relies on it, but here the whole bug being fixed is a scope
// that was wrong silently, so the choice is explicit.
type scanScope struct {
	Namespace string
	All       bool
}

func (s scanScope) selector() []string {
	if s.All {
		return []string{"--all-namespaces"}
	}
	return []string{"-n", s.Namespace}
}

// label is the banner's scope text, matching what kx get -A prints.
func (s scanScope) label() string {
	if s.All {
		return "all namespaces"
	}
	return s.Namespace
}

func (s scanScope) emptyMessage() string {
	if s.All {
		return "no container images found in any namespace."
	}
	return fmt.Sprintf("no container images found in namespace '%s'.", s.Namespace)
}

// ScanCommand resolves container images and hands them to a scanner.
type ScanCommand struct {
	Kubectl kubectl.Service
	State   IndexResolver
	Scanner scanner.Service
	Status  func(string) func()
}

// Execute resolves the unique images of one indexed workload.
func (c ScanCommand) Execute(index int, engine string) ([]string, error) {
	name, namespace, kind, err := c.State.Fields(index)
	if err != nil {
		return nil, err
	}
	if !scannableKinds[kind] {
		return nil, fmt.Errorf("scan is not supported for '%s'.", kind)
	}
	// Validate the engine and its availability before hitting the cluster, so a
	// typo or a missing scanner fails fast with one clear message rather than
	// once per image.
	if _, err := c.EnsureAvailable(engine); err != nil {
		return nil, err
	}

	stop := c.Status("resolving images")
	raw, err := c.Kubectl.Run([]string{"get", string(kind), name, "-n", namespace, "-o", "json"})
	stop()
	if err != nil {
		return nil, err
	}

	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &object); err != nil {
		return nil, err
	}
	images := dedupe(imagesOf(object))
	if len(images) == 0 {
		return nil, fmt.Errorf("no container images found for %s/%s.", kind, name)
	}
	return images, nil
}

// Collect resolves the unique images across every workload in scope.
func (c ScanCommand) Collect(scope scanScope, engine string) ([]string, error) {
	if _, err := c.EnsureAvailable(engine); err != nil {
		return nil, err
	}

	stop := c.Status("resolving images in " + scope.label())
	args := []string{"get", namespaceScanKinds}
	args = append(args, scope.selector()...)
	args = append(args, "-o", "json")
	raw, err := c.Kubectl.Run(args)
	stop()
	if err != nil {
		return nil, err
	}

	var list struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return nil, err
	}
	var images []string
	for _, item := range list.Items {
		images = append(images, imagesOf(item)...)
	}
	images = dedupe(images)
	if len(images) == 0 {
		return nil, errors.New(scope.emptyMessage())
	}
	return images, nil
}

// EnsureAvailable resolves the engine and confirms the scanner is installed, so
// the failure is reported once with a fix rather than per image.
func (c ScanCommand) EnsureAvailable(name string) (scanner.Engine, error) {
	engine, err := scanner.GetEngine(name)
	if err != nil {
		return nil, err
	}
	code, err := c.Scanner.Probe(engine.PreflightArgv())
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("%s", engine.UnavailableMessage())
	}
	return engine, nil
}

// ScanImage streams the scanner's own report for one image.
func (c ScanCommand) ScanImage(engineName, image string, extra []string) (int, error) {
	engine, err := scanner.GetEngine(engineName)
	if err != nil {
		return 1, err
	}
	return c.Scanner.Scan(engine.PassthroughArgv(image, extra))
}

// Summarize scans each image and rolls the results into severity counts.
//
// A failure is recorded on its row rather than aborting: one unpullable image
// shouldn't cost the results for every other image in the namespace.
func (c ScanCommand) Summarize(engineName string, images []string) ([]scanner.ImageScan, error) {
	engine, err := scanner.GetEngine(engineName)
	if err != nil {
		return nil, err
	}

	rows := make([]scanner.ImageScan, 0, len(images))
	for _, image := range images {
		stdout, stderr, code, err := c.Scanner.Capture(engine.SummaryArgv(image))
		if err != nil {
			return nil, err
		}
		if code != 0 {
			rows = append(rows, scanner.ImageScan{Image: image, Error: lastLine(stderr)})
			continue
		}
		findings, err := engine.ParseFindings(stdout)
		if err != nil {
			rows = append(rows, scanner.ImageScan{Image: image, Error: "unparseable output"})
			continue
		}
		rows = append(rows, scanner.ImageScan{
			Image:    image,
			Counts:   scanner.CountBySeverity(findings),
			Findings: findings,
		})
	}
	return rows, nil
}

// lastLine is the most specific part of a scanner's error output; the earlier
// lines are usually progress noise.
func lastLine(text string) string {
	var last string
	for _, line := range strings.Split(text, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			last = trimmed
		}
	}
	if last == "" {
		return "scan failed"
	}
	return last
}

// imagesOf reads the container images out of any workload's PodSpec.
func imagesOf(object map[string]json.RawMessage) []string {
	spec := podSpec(object)
	var images []string
	// Init containers first, matching the order they run in.
	for _, group := range []string{"initContainers", "containers"} {
		var containers []struct {
			Image string `json:"image"`
		}
		if raw, ok := spec[group]; ok {
			_ = json.Unmarshal(raw, &containers)
		}
		for _, container := range containers {
			if container.Image != "" {
				images = append(images, container.Image)
			}
		}
	}
	return images
}

// podSpec locates the PodSpec for any workload kind.
//
// Deployments, StatefulSets, DaemonSets, Jobs and ReplicaSets carry it under
// spec.template.spec; a CronJob nests it one level deeper under
// spec.jobTemplate.spec.template.spec; a bare Pod is spec itself.
func podSpec(object map[string]json.RawMessage) map[string]json.RawMessage {
	spec := decodeObject(object["spec"])
	if kindOf(object) == string(kinds.CronJob) {
		jobTemplate := decodeObject(spec["jobTemplate"])
		jobSpec := decodeObject(jobTemplate["spec"])
		template := decodeObject(jobSpec["template"])
		return decodeObject(template["spec"])
	}
	if template, ok := spec["template"]; ok {
		return decodeObject(decodeObject(template)["spec"])
	}
	return spec
}

func kindOf(object map[string]json.RawMessage) string {
	var kind string
	if raw, ok := object["kind"]; ok {
		_ = json.Unmarshal(raw, &kind)
	}
	return kind
}

func decodeObject(raw json.RawMessage) map[string]json.RawMessage {
	object := map[string]json.RawMessage{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &object)
	}
	return object
}

// dedupe returns the unique images, preserving first-seen order.
func dedupe(images []string) []string {
	seen := map[string]bool{}
	unique := make([]string, 0, len(images))
	for _, image := range images {
		if seen[image] {
			continue
		}
		seen[image] = true
		unique = append(unique, image)
	}
	return unique
}

func imagesNoun(count int) string {
	if count == 1 {
		return "1 image"
	}
	return strconv.Itoa(count) + " images"
}

// scanPage builds the HTML page from the same rows the terminal summary
// renders, so the two views cannot drift apart.
func scanPage(scope string, rows []scanner.ImageScan, meta web.Meta) web.ScanPage {
	return web.ScanPage{Meta: meta, Scope: scope, Images: rows}
}

// sweepPageScope captions a namespace sweep's page with the same "Mixed · "
// cross-kind label render.ScopeBanner already printed to the terminal above
// it. scan.gohtml renders Scope verbatim rather than assuming every scan is a
// sweep — an indexed scan's kind is already known, so its own pageScope (built
// separately, where the index branch is handled) never goes through this and
// carries no such label.
func sweepPageScope(scopeLabel string) string {
	return "Mixed · " + scopeLabel
}

func newScanCommand(services Services) *cobra.Command {
	cmd := &cobra.Command{
		Use: "scan [index] [scanner flags]",
		Short: "Scan the unique container images of an indexed workload for vulnerabilities, " +
			"or a whole namespace when no index is given (-n to pick one, -A for every " +
			"namespace); prints a severity summary table " +
			"by default, or the raw scanner output with --full. Requires the CLI for the " +
			"selected scan engine (Docker Scout by default, https://docs.docker.com/scout/; " +
			"or Trivy via --engine trivy, https://trivy.dev/ — see kx engine).",
		Long: "Resolves the unique container images of a workload and scans each for\n" +
			"vulnerabilities, printing a severity summary table.\n\n" +
			"Requires the CLI for the selected engine. Docker Scout is the default:\n" +
			"https://docs.docker.com/scout/\n" +
			"Trivy is available via --engine trivy: https://trivy.dev/\n" +
			"Run 'kx engine' to see or change the default.",
		Example:            "  kx scan\n  kx scan 1\n  kx scan -n prod\n  kx scan -A\n  kx scan 1 --full",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			rest, handled, err := passthrough(cmd, args, nil)
			if err != nil || handled {
				return err
			}
			engine, rest, err := extractString(rest, "--engine", "")
			if err != nil {
				return err
			}
			if engine == "" {
				engine = services.Config.Engine
			}
			full, rest := extractBool(rest, "--full")
			html, rest := extractBool(rest, "--html")
			noOpen, rest := extractBool(rest, "--no-open")
			portText, rest, err := extractString(rest, "--port", "")
			if err != nil {
				return err
			}
			port := 0
			if portText != "" {
				if port, err = strconv.Atoi(portText); err != nil {
					return fmt.Errorf(
						"Invalid value for '--port': '%s' is not a valid int.", portText)
				}
			}
			if full && html {
				return errors.New(
					"'--full' cannot be combined with '--html' — the HTML report " +
						"already carries every finding.")
			}
			htmlOpts := htmlOptions{Enabled: html, Port: port, NoOpen: noOpen}

			// Presence is checked before extractString consumes the flag:
			// `-n ""` is a namespace flag the guards below still have to see,
			// and the returned value alone cannot tell it from an absent one.
			hasNamespace := hasFlag(rest, "--namespace", "-n")
			namespace, rest, err := extractString(rest, "--namespace", "-n")
			if err != nil {
				return err
			}
			all, rest := extractBool(rest, "--all-namespaces", "-A")
			if hasNamespace && all {
				return errors.New(
					"'--all-namespaces' and '--namespace' cannot be combined.")
			}

			command := ScanCommand{
				Kubectl: services.Kubectl,
				State:   services.State,
				Scanner: scanner.ExecService{},
				Status:  render.Status,
			}

			// A leading index selects one workload; without it the whole
			// namespace is swept. Anything after belongs to the scanner.
			indexArgs, extra := splitLeadingIndexes(rest)
			// Scanner flags start with a dash, so a bare non-numeric first
			// argument is a mistyped index. Sweeping the namespace instead
			// would ignore what was typed and act on something else — and
			// outside --full the stray argument is dropped entirely.
			if len(indexArgs) == 0 && len(extra) > 0 && !strings.HasPrefix(extra[0], "-") {
				return fmt.Errorf(
					"Invalid value for 'index': '%s' is not a valid int.", extra[0])
			}
			// An index already carries the namespace it was listed from, so a
			// scope flag next to one is a contradiction rather than a refinement.
			scopeFlag := ""
			if hasNamespace {
				scopeFlag = "--namespace"
			}
			if all {
				scopeFlag = "--all-namespaces"
			}
			if len(indexArgs) > 0 && scopeFlag != "" {
				return fmt.Errorf(
					"'%s' cannot be combined with an index — an index already "+
						"carries the namespace it was listed from. Drop the flag, "+
						"or drop the index to sweep the namespace instead.", scopeFlag)
			}
			// pageScope captions the HTML page. Captured in each branch
			// because an indexed scan is scoped by the workload it resolved
			// rather than by the namespace being swept.
			//
			// pageTitle is the browser tab's name, which wants the bare
			// subject and not the caption's "Mixed" kind label — a tab
			// reading "kx scan · Mixed · prod" says less than "kx scan ·
			// prod". kx diag titles itself the same way.
			var pageScope, pageTitle string
			var images []string
			if len(indexArgs) == 0 {
				scope := scanScope{Namespace: namespace, All: all}
				if !scope.All && scope.Namespace == "" {
					scope.Namespace = services.Kubectl.CurrentNamespace()
				}
				// Reassigned so the invocation line built below (scopeArgs)
				// names the resolved namespace, not whatever -n was left as —
				// which is empty on the common path where it defaults from
				// the current context. diagnostic.go's namespace resolution
				// does the same (reassigns before building its own
				// invocation line) for the same reason.
				namespace = scope.Namespace
				images, err = command.Collect(scope, engine)
				if err != nil {
					return err
				}
				render.ScopeBanner("Mixed", scope.label(), imagesNoun(len(images)))
				pageScope = sweepPageScope(scope.label())
				pageTitle = scope.label()
			} else {
				index, err := parseIndex("index", indexArgs[0])
				if err != nil {
					return err
				}
				name, resourceNamespace, kind, err := services.State.Fields(index)
				if err != nil {
					return err
				}
				images, err = command.Execute(index, engine)
				if err != nil {
					return err
				}
				render.Banner(string(kind), name, resourceNamespace, imagesNoun(len(images)))
				pageScope = string(kind) + "/" + name + " · " + resourceNamespace
				pageTitle = string(kind) + "/" + name
			}

			if full {
				for position, image := range images {
					if position > 0 {
						render.Raw("")
					}
					render.Section(image)
					if _, err := command.ScanImage(engine, image, extra); err != nil {
						return err
					}
				}
				return nil
			}

			stop := render.Status("scanning")
			rows, err := command.Summarize(engine, images)
			stop()
			if err != nil {
				return err
			}
			render.ScanSummary(rows)
			if !htmlOpts.Enabled {
				return nil
			}
			indexArg := ""
			if len(indexArgs) > 0 {
				indexArg = indexArgs[0]
			}
			meta, err := pageMeta(services.Config.Theme, "kx scan · "+pageTitle,
				invocation("scan", indexArg, scopeArgs(namespace, all), portFlag(port)))
			if err != nil {
				return err
			}
			page, err := web.RenderScan(scanPage(pageScope, rows, meta))
			if err != nil {
				return err
			}
			return servePage(cmd.Context(), page, htmlOpts)
		},
	}
	// Registered so they appear in the command's help; parsing is by hand.
	cmd.Flags().String("engine", "",
		"Vulnerability scanner to use; run 'kx engine' to see available engines and the configured default")
	cmd.Flags().Bool("full", false,
		"Stream the scanner's full output instead of the summary table")
	cmd.Flags().StringP("namespace", "n", "",
		"Namespace to sweep; defaults to the current namespace")
	cmd.Flags().BoolP("all-namespaces", "A", false,
		"Sweep every namespace")
	cmd.Flags().Bool("html", false,
		"Render the report as HTML and serve it in a browser")
	cmd.Flags().Int("port", 0,
		"Port to serve the HTML report on; 0 picks a free one")
	cmd.Flags().Bool("no-open", false,
		"Serve the HTML report without opening a browser")
	return cmd
}
