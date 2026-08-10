package diagnostics

import (
	"context"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/jzills/kx/internal/graph"
	"github.com/jzills/kx/internal/kinds"
)

// Sweep diagnoses every workload in a namespace, plus orphan pods — pods not
// owned by anything swept, such as bare pods. An empty namespace sweeps every
// namespace, which is how client-go's listers already spell it.
//
// Each result takes its namespace from the object rather than from the
// argument, so a cluster-wide sweep labels its rows correctly instead of
// stamping them all with the empty string it was called with.
//
// One list call per kind and one events fetch. No log tails: logs feed the
// detailed LOGS section only, never findings, so the sweep skips them entirely
// for speed.
func (s Service) Sweep(ctx context.Context, namespace string) ([]Data, error) {
	list := metav1.ListOptions{}

	pods, err := s.Client.CoreV1().Pods(namespace).List(ctx, list)
	if err != nil {
		return nil, err
	}
	replicaSets, err := s.Client.AppsV1().ReplicaSets(namespace).List(ctx, list)
	if err != nil {
		return nil, err
	}
	jobs, err := s.Client.BatchV1().Jobs(namespace).List(ctx, list)
	if err != nil {
		return nil, err
	}
	allEvents, err := s.Events.Get(ctx, namespace)
	if err != nil {
		return nil, err
	}
	// One metrics call for the whole sweep. Unlike logs, usage actually drives
	// findings, so every entry needs it — but it is still a single call.
	usage := s.usageLookup(ctx, namespace)

	var results []Data
	claimedPods := map[types.UID]bool{}
	claimedJobs := map[types.UID]bool{}

	// CronJobs own Jobs rather than pods, so they get their own pass. Their
	// owned Jobs are excluded from the standalone Job listing below so a
	// CronJob-owned Job is never reported twice — once under its CronJob and
	// once as its own row.
	cronJobs, err := s.Client.BatchV1().CronJobs(namespace).List(ctx, list)
	if err != nil {
		return nil, err
	}
	for i := range cronJobs.Items {
		cronJob := &cronJobs.Items[i]
		ownedJobUIDs := map[types.UID]bool{}
		for j := range jobs.Items {
			if graph.OwnedBy(jobs.Items[j].ObjectMeta, cronJob.UID) {
				ownedJobUIDs[jobs.Items[j].UID] = true
				claimedJobs[jobs.Items[j].UID] = true
			}
		}
		// Every owned Job's pods are claimed, not just the displayed
		// (most-recent) one's: all owned Jobs are excluded from the standalone
		// listing, so all of their pods must be excluded from the orphan pass
		// too, or old runs' pods leak through as unlabelled orphans.
		for j := range pods.Items {
			for _, ref := range pods.Items[j].OwnerReferences {
				if ownedJobUIDs[ref.UID] {
					claimedPods[pods.Items[j].UID] = true
				}
			}
		}

		health := &CronJobHealth{Suspended: cronJob.Spec.Suspend != nil && *cronJob.Spec.Suspend}
		var recentPods []corev1.Pod
		if recent := graph.MostRecentJob(cronJob.UID, jobs.Items); recent != nil {
			health.MostRecentJob = jobHealthFrom(recent)
			recentPods = ownedPods(pods.Items, map[types.UID]bool{recent.UID: true})
		}

		data := Data{
			Kind: kinds.CronJob, Name: cronJob.Name, Namespace: cronJob.Namespace,
			CronJob: health,
			Pods:    diagnoseAll(recentPods),
		}
		attachUsage(data.Pods, data.Namespace, usage)
		data.WarningEvents = s.warningEvents(
			kinds.CronJob, cronJob.Name, data.Namespace, recentPods, allEvents)
		results = append(results, data)
	}

	// Workload controllers, each claiming the pods it owns.
	type workload struct {
		kind      kinds.Kind
		name      string
		namespace string
		object    any
		owners    map[types.UID]bool
	}
	var workloads []workload

	deployments, err := s.Client.AppsV1().Deployments(namespace).List(ctx, list)
	if err != nil {
		return nil, err
	}
	for i := range deployments.Items {
		deployment := &deployments.Items[i]
		owners := map[types.UID]bool{}
		for j := range replicaSets.Items {
			if graph.OwnedBy(replicaSets.Items[j].ObjectMeta, deployment.UID) {
				owners[replicaSets.Items[j].UID] = true
			}
		}
		workloads = append(workloads, workload{kinds.Deployment, deployment.Name, deployment.Namespace, deployment, owners})
	}

	statefulSets, err := s.Client.AppsV1().StatefulSets(namespace).List(ctx, list)
	if err != nil {
		return nil, err
	}
	for i := range statefulSets.Items {
		object := &statefulSets.Items[i]
		workloads = append(workloads, workload{
			kinds.StatefulSet, object.Name, object.Namespace, object,
			map[types.UID]bool{object.UID: true}})
	}

	daemonSets, err := s.Client.AppsV1().DaemonSets(namespace).List(ctx, list)
	if err != nil {
		return nil, err
	}
	for i := range daemonSets.Items {
		object := &daemonSets.Items[i]
		workloads = append(workloads, workload{
			kinds.DaemonSet, object.Name, object.Namespace, object,
			map[types.UID]bool{object.UID: true}})
	}

	for i := range jobs.Items {
		job := &jobs.Items[i]
		if claimedJobs[job.UID] {
			continue
		}
		workloads = append(workloads, workload{
			kinds.Job, job.Name, job.Namespace, job,
			map[types.UID]bool{job.UID: true}})
	}

	for _, entry := range workloads {
		owned := ownedPods(pods.Items, entry.owners)
		for i := range owned {
			claimedPods[owned[i].UID] = true
		}
		data := Data{
			Kind: entry.kind, Name: entry.name, Namespace: entry.namespace,
			Pods: diagnoseAll(owned),
		}
		data.Replicas = replicaHealthFrom(entry.kind, entry.object)
		if entry.kind == kinds.Job {
			data.Job = jobHealthFrom(entry.object.(*batchv1.Job))
		}
		attachUsage(data.Pods, data.Namespace, usage)
		data.WarningEvents = s.warningEvents(
			entry.kind, entry.name, entry.namespace, owned, allEvents)
		results = append(results, data)
	}

	// Services match pods by label selector, not ownership. Their pods are
	// independently owned (or genuinely unowned) elsewhere, so a Service never
	// claims a pod out of the orphan pass.
	endpointsList, err := s.Client.CoreV1().Endpoints(namespace).List(ctx, list)
	if err != nil {
		return nil, err
	}
	// Namespace-qualified for the same reason usageKey is: a Service shares its
	// name with the Endpoints object, and that name repeats across namespaces.
	// Keyed by name alone, a cluster-wide sweep would give both Services
	// whichever Endpoints was listed last — and no endpoints is a Critical
	// finding, so a broken Service would be scored healthy and never shown.
	type endpointsKey struct{ namespace, name string }
	endpointsFor := map[endpointsKey]*corev1.Endpoints{}
	for i := range endpointsList.Items {
		item := &endpointsList.Items[i]
		endpointsFor[endpointsKey{item.Namespace, item.Name}] = item
	}

	services, err := s.Client.CoreV1().Services(namespace).List(ctx, list)
	if err != nil {
		return nil, err
	}
	for i := range services.Items {
		service := &services.Items[i]
		var matched []corev1.Pod
		if len(service.Spec.Selector) > 0 {
			for j := range pods.Items {
				// A selector reaches only its own namespace: app=web means
				// something different in each one, so a cluster-wide sweep must
				// not hand this Service another namespace's pods.
				if pods.Items[j].Namespace != service.Namespace {
					continue
				}
				if graph.MatchesSelector(pods.Items[j], service.Spec.Selector) {
					matched = append(matched, pods.Items[j])
				}
			}
		}
		data := Data{
			Kind: kinds.Service, Name: service.Name, Namespace: service.Namespace,
			Service: serviceHealthFrom(
				service, endpointsFor[endpointsKey{service.Namespace, service.Name}]),
			Pods: diagnoseAll(matched),
		}
		attachUsage(data.Pods, data.Namespace, usage)
		data.WarningEvents = s.warningEvents(
			kinds.Service, service.Name, service.Namespace, matched, allEvents)
		results = append(results, data)
	}

	// PVCs have no pods and no ownership — just a listing and a phase check.
	claims, err := s.Client.CoreV1().PersistentVolumeClaims(namespace).List(ctx, list)
	if err != nil {
		return nil, err
	}
	for i := range claims.Items {
		claim := &claims.Items[i]
		results = append(results, Data{
			Kind: kinds.PersistentVolumeClaim, Name: claim.Name, Namespace: claim.Namespace,
			PVC: &PVCHealth{Phase: phaseOr(string(claim.Status.Phase))},
			WarningEvents: s.warningEvents(
				kinds.PersistentVolumeClaim, claim.Name, claim.Namespace, nil, allEvents),
		})
	}

	// Ingresses have no pods and no ownership — a structural check against
	// the Services list already fetched above for the Service kind's own
	// block. Namespace-qualified for the same reason usageKey/endpointsKey
	// are: a cluster-wide sweep sees same-named Services across namespaces,
	// and an Ingress's backend only ever resolves within its own.
	type serviceKey struct{ namespace, name string }
	knownServices := make(map[serviceKey]bool, len(services.Items))
	for i := range services.Items {
		knownServices[serviceKey{services.Items[i].Namespace, services.Items[i].Name}] = true
	}
	ingresses, err := s.Client.NetworkingV1().Ingresses(namespace).List(ctx, list)
	if err != nil {
		return nil, err
	}
	for i := range ingresses.Items {
		ingress := &ingresses.Items[i]
		var missing []string
		for _, backendName := range ingressBackendServiceNames(ingress) {
			if !knownServices[serviceKey{ingress.Namespace, backendName}] {
				missing = append(missing, backendName)
			}
		}
		results = append(results, Data{
			Kind: kinds.Ingress, Name: ingress.Name, Namespace: ingress.Namespace,
			Ingress: &IngressHealth{MissingBackends: missing},
			WarningEvents: s.warningEvents(
				kinds.Ingress, ingress.Name, ingress.Namespace, nil, allEvents),
		})
	}

	// Whatever is left: pods nothing swept claimed.
	for i := range pods.Items {
		pod := &pods.Items[i]
		if claimedPods[pod.UID] {
			continue
		}
		single := []corev1.Pod{*pod}
		data := Data{
			Kind: kinds.Pod, Name: pod.Name, Namespace: pod.Namespace,
			Pods: diagnoseAll(single),
		}
		attachUsage(data.Pods, data.Namespace, usage)
		data.WarningEvents = s.warningEvents(kinds.Pod, pod.Name, pod.Namespace, single, allEvents)
		results = append(results, data)
	}

	return results, nil
}

func ownedPods(pods []corev1.Pod, owners map[types.UID]bool) []corev1.Pod {
	var owned []corev1.Pod
	for i := range pods {
		for _, ref := range pods[i].OwnerReferences {
			if owners[ref.UID] {
				owned = append(owned, pods[i])
				break
			}
		}
	}
	return owned
}

func diagnoseAll(pods []corev1.Pod) []PodDiagnostic {
	diagnostics := make([]PodDiagnostic, 0, len(pods))
	for i := range pods {
		diagnostics = append(diagnostics, podDiagnostic(&pods[i]))
	}
	return diagnostics
}
