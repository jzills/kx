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

// Data is the raw, already-flattened result of a gather. It carries no
// findings — the analysis layer produces those.
type Data struct {
	Kind          kinds.Kind
	Name          string
	Namespace     string
	Replicas      *ReplicaHealth
	Job           *JobHealth
	Service       *ServiceHealth
	PVC           *PVCHealth
	CronJob       *CronJobHealth
	Ingress       *IngressHealth
	Pods          []PodDiagnostic
	WarningEvents []EventSummary
}

// Finding is one distilled health signal.
type Finding struct {
	Severity Severity
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
