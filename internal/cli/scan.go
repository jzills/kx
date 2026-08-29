package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"

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
		return render.AllNamespaces
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
	// An absent binary and a preflight that runs and fails are the same
	// condition to a user — the scanner isn't usable — and the engine's own
	// message is the only one that names the CLI and where to get it. Routing
	// the absent case here as well is what makes every engine report the way
	// Docker Scout always has: its preflight runs `docker`, which is present
	// and merely answers "unknown command", so it never took the other path.
	//
	// It also keeps the failure out of the stale-state machinery: isStale
	// falls back to IsNotFound, which matches a bare "not found" anywhere in
	// an error, and NotFoundError's own wording ("grype not found on PATH")
	// contains it.
	var notFound scanner.NotFoundError
	if err != nil && !errors.As(err, &notFound) {
		return nil, err
	}
	if err != nil || code != 0 {
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

// scanWorkers bounds how many scanners run at once.
//
// Two, and deliberately not one per CPU. A scanner unpacks an image and walks
// every package in it, so what limits it is memory, not cores: Grype peaks
// around 325MB of RSS per image. Measured on a 16-core machine with 7GB of RAM,
// sweeping this project's test namespace with Grype:
//
//	workers   round 1   round 2
//	      1       58s       13s
//	      2        6s        6s
//	      4        7s       16s
//
// Four is faster than serial when there is memory to spare and slower than
// serial when there is not — 1.3GB of scanners on a box with 2GB free spends
// the difference in reclaim. Two beat both, consistently, and it is the number
// that degrades gracefully on a machine smaller than the one it was tuned on.
// The measurements are noisy (serial ranged 13s to 58s across identical runs)
// but the ordering held.
//
// Fixed rather than a flag because nobody has needed to tune it; a knob can be
// added when someone does.
const scanWorkers = 2

// Summarize scans each image and rolls the results into severity counts.
//
// Images are scanned concurrently: a sweep is almost entirely spent waiting on
// a scanner, and doing them one at a time made a real cluster's worth of images
// a minutes-long wait for work that overlaps freely. Results are written by
// position rather than appended, so the rows come back in the order the images
// were resolved in whatever order the scans finish — the table is indexed by
// position, and callers pin that order.
//
// onScanned, if given, is called once per image as it completes, from the
// scanning goroutine — it is what turns the spinner into a count, and must be
// safe to call concurrently.
//
// A scanner failure is recorded on its row rather than aborting: one unpullable
// image shouldn't cost the results for every other image in the namespace. A
// failure that is not the scanner's exit code — the binary missing — still
// aborts, reporting the earliest image's error, which is the one the serial
// version stopped on.
func (c ScanCommand) Summarize(
	engineName string, images []string, onScanned func(),
) ([]scanner.ImageScan, error) {
	engine, err := scanner.GetEngine(engineName)
	if err != nil {
		return nil, err
	}

	rows := make([]scanner.ImageScan, len(images))
	failures := make([]error, len(images))

	// A fixed pool draining a channel of positions, rather than a goroutine per
	// image parked on a semaphore. The bound is scanWorkers either way, but a
	// namespace sweep resolves hundreds of unique images, and the semaphore
	// shape launched a goroutine for every one of them — all but two spending
	// the whole sweep blocked on a slot, for nothing. Making the pool the
	// structure also means the bound is not something each goroutine has to
	// remember to take.
	positions := make(chan int)
	var group sync.WaitGroup
	for range min(scanWorkers, len(images)) {
		group.Add(1)
		go func() {
			defer group.Done()
			for position := range positions {
				// Each position is handled by exactly one worker, so the two
				// slices are written without overlap and neither needs a lock.
				rows[position], failures[position] = c.scanImage(engine, images[position])
				if onScanned != nil {
					onScanned()
				}
			}
		}()
	}
	for position := range images {
		positions <- position
	}
	close(positions)
	group.Wait()

	// In order, so the reported failure is the same one a serial sweep would
	// have stopped on rather than whichever goroutine happened to finish first.
	for _, failure := range failures {
		if failure != nil {
			return nil, failure
		}
	}
	return rows, nil
}

// scanImage is one image's scan. The error return is reserved for a failure
// that is not the scanner's own verdict; anything the scanner reported lands on
// the row.
func (c ScanCommand) scanImage(engine scanner.Engine, image string) (scanner.ImageScan, error) {
	stdout, stderr, code, err := c.Scanner.Capture(engine.SummaryArgv(image))
	if err != nil {
		return scanner.ImageScan{}, err
	}
	if code != 0 {
		return scanner.ImageScan{Image: image, Error: lastLine(stderr)}, nil
	}
	findings, err := engine.ParseFindings(stdout)
	if err != nil {
		return scanner.ImageScan{Image: image, Error: "unparseable output"}, nil
	}
	return scanner.ImageScan{
		Image:    image,
		Counts:   scanner.CountBySeverity(findings),
		Findings: findings,
	}, nil
}

// ansiEscape matches the CSI sequences a scanner uses to colour its own
// output. Grype colours stderr; its reset lands mid-cell in kx's summary
// table, ending kx's styling early and printing the escape's tail as text.
var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

// lastLine is the most specific part of a scanner's error output; the earlier
// lines are usually progress noise.
//
// Styling is stripped rather than preserved: the string is bound for a cell kx
// styles itself, and a scanner's own colours cannot know what they are landing
// in. A line that was nothing but escapes is treated as empty, so the fallback
// covers it instead of a cell rendering blank.
func lastLine(text string) string {
	var last string
	for _, line := range strings.Split(text, "\n") {
		if trimmed := strings.TrimSpace(ansiEscape.ReplaceAllString(line, "")); trimmed != "" {
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
		Use:        "scan [index] [scanner flags]",
		SuggestFor: []string{"cve", "cves", "vuln", "vulns", "vulnerabilities"},
		Short: "Scan the unique container images of an indexed workload for vulnerabilities, " +
			"or a whole namespace when no index is given (-n to pick one, -A for every " +
			"namespace); prints a severity summary table " +
			"by default, or the raw scanner output with --full. Requires the CLI for the " +
			"selected scan engine (Docker Scout by default; Trivy or Grype via " +
			"--engine — see kx engine).",
		Long: "Resolves the unique container images of a workload and scans each for vulnerabilities, printing a severity summary table.\n\n" +
			"Requires the CLI for the selected engine. Docker Scout is the default: https://docs.docker.com/scout/\n" +
			"Trivy is available via --engine trivy: https://trivy.dev/\n" +
			"Grype is available via --engine grype: https://github.com/anchore/grype\n" +
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
			asJSON, rest := extractBool(rest, "--json")
			failOn, rest, err := extractString(rest, "--fail-on", "")
			if err != nil {
				return err
			}
			noOpen, rest := extractBool(rest, "--no-open")
			hasPort := hasFlag(rest, "--port", "")
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
			if asJSON && html {
				return errors.New(
					"'--json' cannot be combined with '--html' — one is for a " +
						"machine and the other for a browser.")
			}
			if asJSON && full {
				return errors.New(
					"'--json' cannot be combined with '--full' — --full streams " +
						"the scanner's own report, which kx does not parse.")
			}
			// Same reason, one step further: a gate needs findings, and --full
			// leaves kx with none to read. Refused rather than quietly ignored,
			// which is what this did — a pipeline that added --full went green
			// whatever the scan turned up, and nothing said the gate had gone.
			if full && failOn != "" {
				return errors.New(
					"'--full' cannot be combined with '--fail-on' — --full streams " +
						"the scanner's own report, which kx does not parse, so the " +
						"gate has nothing to read.")
			}
			// Validated up front so a typo fails before any image is scanned
			// rather than after every one of them has been.
			if failOn != "" {
				if _, err := scanThresholdBreached(nil, failOn); err != nil {
					return err
				}
			}
			htmlOpts := htmlOptions{Enabled: html, Port: port, NoOpen: noOpen}
			if err := htmlOpts.validate(hasPort, noOpen); err != nil {
				return err
			}

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
				Scanner: services.scannerService(),
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
			// reading "scan · Mixed · prod" says less than "scan · prod".
			// kx diag titles itself the same way.
			var pageScope, pageTitle string
			// subject is what --json says the scan was about. Built beside the
			// page labels rather than derived from them: those are display
			// strings, and the whole point of the struct is that a machine
			// never has to read one.
			var subject scanSubject
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
				if !asJSON {
					render.ScopeBanner("Mixed", scope.label(), imagesNoun(len(images)))
				}
				pageScope = sweepPageScope(scope.label())
				pageTitle = scope.label()
				subject = scanSubject{AllNamespaces: scope.All}
				if !scope.All {
					subject.Namespace = scope.Namespace
				}
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
				if !asJSON {
					render.Banner(string(kind), name, resourceNamespace, imagesNoun(len(images)))
				}
				pageScope = string(kind) + "/" + name + " · " + resourceNamespace
				pageTitle = string(kind) + "/" + name
				subject = scanSubject{Kind: kind, Name: name, Namespace: resourceNamespace}
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

			advance, stop := render.Progress("scanning", len(images))
			rows, err := command.Summarize(engine, images, advance)
			stop()
			if err != nil {
				return err
			}
			if asJSON {
				document, err := scanJSON(subject, rows)
				if err != nil {
					return err
				}
				render.Raw(document)
				return scanGate(rows, failOn)
			}
			render.ScanSummary(rows)
			// The gate is the tail of every path, not the alternative to one.
			// Publishing a report and failing the build are not in conflict:
			// --html says where the findings go, --fail-on says what they mean.
			if htmlOpts.Enabled {
				indexArg := ""
				if len(indexArgs) > 0 {
					indexArg = indexArgs[0]
				}
				meta, err := pageMeta(services.Config.Theme, "scan · "+pageTitle,
					invocation("scan", indexArg, scopeArgs(namespace, all), portFlag(port)))
				if err != nil {
					return err
				}
				page, err := web.RenderScan(scanPage(pageScope, rows, meta))
				if err != nil {
					return err
				}
				// After the server stops, so Ctrl-C ends the command with the
				// exit code the scan earned rather than the server's nil.
				if err := servePage(cmd.Context(), page, htmlOpts); err != nil {
					return err
				}
			}
			return scanGate(rows, failOn)
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
	cmd.Flags().Bool("json", false,
		"Print the severity counts and every finding as JSON instead of a table")
	cmd.Flags().String("fail-on", "",
		"Exit 2 when any image carries a vulnerability at this severity or worse "+
			"(critical, high, medium, low)")
	return cmd
}

// scanGate turns a scan into an exit code when --fail-on asked for one.
//
// SilentError because the summary has already been printed: this is the exit
// code, not a second error message.
func scanGate(rows []scanner.ImageScan, failOn string) error {
	if failOn == "" {
		return nil
	}
	breached, err := scanThresholdBreached(rows, failOn)
	if err != nil {
		return err
	}
	if !breached {
		return nil
	}
	return SilentError{Code: findingsExitCode}
}
