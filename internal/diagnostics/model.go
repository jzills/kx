// Package diagnostics gathers health signals for a workload and distils them
// into findings.
//
// Gathering and analysis are deliberately separate: the service produces flat
// data from the API, and the findings layer turns it into a verdict. That keeps
// the thresholds testable without a cluster, and keeps the API-shaped code free
// of judgement calls.
package diagnostics

import (
	"time"

	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/jzills/kx/internal/kinds"
)

// Severity is ordered so the highest finding gives the overall verdict.
type Severity int

const (
	OK Severity = iota
	Warning
	Critical
)

func (s Severity) String() string {
	switch s {
	case Critical:
		return "critical"
	case Warning:
		return "warnings"
	default:
		return "healthy"
	}
}

// Token is the machine-readable spelling, for --json.
//
// Singular where String() is plural. A verdict is a headline on screen —
// "Deployment/api · warnings" — and the plural reads correctly there. In a
// document it is the value of a severity field, and one finding does not have
// severity "warnings". The singular is also the spelling --fail-on documents,
// so a value read out of the JSON can be typed straight back at the gate.
func (s Severity) Token() string {
	if s == Warning {
		return "warning"
	}
	return s.String()
}

// ContainerDiagnostic is one container's flattened status.
type ContainerDiagnostic struct {
	Name                 string
	Ready                bool
	Started              *bool
	RestartCount         int32
	State                string // "Running" | "Waiting" | "Terminated" | "Unknown"
	WaitingReason        string
	WaitingMessage       string
	TerminatedReason     string
	ExitCode             *int32
	LastTerminatedReason string
	LastExitCode         *int32
	LogLines             []string
	LogSource            string // "previous" | "current"
	// LogFiltered is false when LogLines is a raw tail rather than lines
	// matching a severity token.
	LogFiltered bool
	CPUUsage    *resource.Quantity
	CPULimit    *resource.Quantity
	MemoryUsage *resource.Quantity
	MemoryLimit *resource.Quantity
}

// SchedulingInfo records why a pod could not be placed.
type SchedulingInfo struct {
	Schedulable bool
	Reason      string
	Message     string
}

// PodDiagnostic is one pod's flattened status.
type PodDiagnostic struct {
	Name            string
	Phase           string
	Node            string
	ReadyContainers int
	TotalContainers int
	Containers      []ContainerDiagnostic
	Scheduling      SchedulingInfo
}

// ReplicaHealth is the replica rollup shared by Deployments, StatefulSets and
// DaemonSets.
type ReplicaHealth struct {
	Desired            int32
	Ready              int32
	Available          int32
	Updated            int32
	Generation         *int64
	ObservedGeneration *int64
}

// ServiceHealth is built from the Service's Endpoints, not its own spec — the
// Service carries no health signal, and Endpoints is the same source the
// cluster uses to decide where traffic routes.
type ServiceHealth struct {
	HasSelector       bool
	ReadyAddresses    int
	NotReadyAddresses int
}

// PVCHealth is self-contained: no pod fan-out, no ownership.
type PVCHealth struct {
	Phase string // "Pending" | "Bound" | "Lost" | "Unknown"
}

// JobHealth does not reuse ReplicaHealth: a Job has no desired/ready replica
// concept, only completion and failure counts against a backoff limit.
type JobHealth struct {
	Succeeded            int32
	Failed               int32
	Active               int32
	Suspended            bool
	BackoffLimit         int32
	BackoffLimitExceeded bool
	DeadlineExceeded     bool
}

// CronJobHealth rolls up the most recently run owned Job.
//
// No cron-expression parsing: the semantics are easy to get subtly wrong
// (timezones, concurrencyPolicy) and a wrong "missed schedule" finding is worse
// than no finding.
type CronJobHealth struct {
	Suspended     bool
	MostRecentJob *JobHealth
}

// IngressHealth is a structural check: do the Services an Ingress's rules
// point at actually exist? Kubernetes has no loadBalancer-status signal
// that's portable across ingress controllers (some never populate it even
// when healthy), so that's deliberately not checked here — see CronJobHealth
// above for the same reasoning applied to cron-expression parsing.
type IngressHealth struct {
	// MissingBackends are the referenced Service names (deduped, sorted) that
	// do not exist in the Ingress's namespace. Empty means every backend
	// resolved.
	MissingBackends []string
}

// EventSummary is a grouped warning event.
type EventSummary struct {
	Reason        string
	Message       string
	Kind          string
	Name          string
	Count         int32
	LastTimestamp time.Time
}

// NodeCondition is one of a Node's status conditions, flattened to what a
// finding needs. Status is the raw "True"/"False"/"Unknown" tri-state rather
// than a bool: "Unknown" is how a Node that stopped reporting looks, and it is
// not the same as False for the Ready condition.
type NodeCondition struct {
	Type    string
	Status  string
	Reason  string
	Message string
}

// PodPhaseCounts is a tally of the pods on a Node, by phase.
//
// Counts rather than the per-pod table the workload kinds carry: a real node
// runs hundreds of pods, and a table that long is not a diagnosis. Naming the
// pods that are not running is one `kx get pods --field-selector` away, and the
// finding says so.
type PodPhaseCounts struct {
	Total     int
	Running   int
	Succeeded int
	Pending   int
	Failed    int
	Unknown   int
}

// Stalled is the pods that are neither running nor finished — the ones worth
// a line. Succeeded is excluded deliberately: a Job's pod stays on the node
// after it completes, and counting those as "not running" would report every
// node that has ever run a CronJob as degraded.
//
// Failed is excluded for exactly the same reason, which the original wording
// gave and then did not act on. Kubernetes keeps a terminated pod's object on
// the node until the terminated-pod GC threshold — 12500 by default — so one
// preemption or OOM eviction left a node reporting "1/40 pods not running"
// indefinitely, its verdict never returning to healthy, and (since --fail-on
// shipped) failing a CI gate forever on a cluster with nothing wrong with it.
//
// The pod itself is not lost: a pod that failed is a fact about the workload
// that owns it, and kx diag on that workload reports it. A node is not the
// right place to be told about something that finished days ago.
func (c PodPhaseCounts) Stalled() int { return c.Pending + c.Unknown }

// Active is the pods the node is still expected to be running: everything
// except the ones that have terminated.
//
// This is Stalled's denominator, and has to be. Stalled excludes Succeeded and
// Failed for the reasons above; a ratio whose numerator excludes them but
// whose denominator counts them is false to anyone who checks. A node with 23
// running, 5 failed and 1 pending reported "1/29 pods not running" when six of
// the 29 were not running. It reports "1/24" now, and 23 of those 24 are
// indeed running.
//
// Summed from the parts rather than subtracted from Total, which podPhaseCounts
// guarantees comes to the same thing: a phase Kubernetes does not name is
// counted as Unknown rather than dropped, so these three are exactly the
// non-terminated pods.
func (c PodPhaseCounts) Active() int { return c.Running + c.Pending + c.Unknown }

// NodeHealth is a Node's conditions, whether it has been cordoned, and what is
// scheduled on it.
type NodeHealth struct {
	Conditions    []NodeCondition
	Unschedulable bool
	Pods          PodPhaseCounts
}

// Data is the raw, already-flattened result of a gather. It carries no
// findings — the analysis layer produces those.
type Data struct {
	Kind          kinds.Kind
	Name          string
	Namespace     string
	Node          *NodeHealth
	Replicas      *ReplicaHealth
	Job           *JobHealth
	Service       *ServiceHealth
	PVC           *PVCHealth
	CronJob       *CronJobHealth
	Ingress       *IngressHealth
	Pods          []PodDiagnostic
	WarningEvents []EventSummary
}

// Rank orders findings of equal severity by how specific they are.
//
// The triage table shows one finding per row, so which of several equally
// severe findings sorts first decides what a whole sweep reads like. A
// Deployment whose only pod is crashlooping produces both "Only 0/1 replicas
// ready" and "CrashLoopBackOff in pod x" at Critical; the first is true of
// every broken Deployment and the second is why this one is broken.
//
// Values ascend from most specific to least, so the zero value would be Cause
// — which is why Finding's fields are set positionally at every construction
// site rather than defaulted: an unranked finding claiming to name a cause is
// exactly the mistake this type exists to prevent.
type Rank int

const (
	// Cause names a concrete failure: a container state, a scheduling
	// refusal, an exceeded limit, a missing backend.
	Cause Rank = iota
	// Aggregate rolls several things up — replica counts, endpoint counts,
	// a pod's ready-container tally. True of the resource, but it describes
	// the shape of the problem rather than its origin.
	Aggregate
	// Event is a raw warning event. The weakest headline available: the
	// report's WARNING EVENTS section already prints it in full, and it
	// usually restates a symptom a pod-level finding has explained.
	Event
)

// Finding is one distilled health signal.
type Finding struct {
	Severity Severity
	Rank     Rank
	Summary  string
}

// Report is the analysed result.
//
// Replica health is not carried here: the findings distil it into ready,
// available and updated shortfalls, so the rendered report needs no separate
// replica section.
type Report struct {
	Kind          kinds.Kind
	Name          string
	Namespace     string
	Verdict       Severity
	Findings      []Finding
	Pods          []PodDiagnostic
	WarningEvents []EventSummary
}
