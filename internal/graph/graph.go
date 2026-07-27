// Package graph walks Kubernetes ownership references to build the tree kx
// renders.
//
// This is the one place kx needs the API server rather than kubectl: ownership
// references are not in kubectl's table output, and reconstructing them from
// `-o json` per resource would be many more round trips than listing each kind
// once.
package graph

import (
	"context"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/theme"
)

// Resource is a node recorded for indexing, so `kx tree --index` can save the
// tree as state and later commands can resolve those indexes.
type Resource struct {
	Name string
	Kind kinds.Kind
}

// Builder walks ownership references against a Kubernetes API client.
type Builder struct {
	Client kubernetes.Interface
}

// nodeStyle gives the prefix and semantic style for each kind in a tree.
// Controllers render with their full kind name; owned resources use the short
// prefixes (rs/job/pod) the single-resource tree uses.
var nodeStyle = map[kinds.Kind]struct{ Prefix, Style string }{
	kinds.Deployment:  {"Deployment", theme.Header},
	kinds.StatefulSet: {"StatefulSet", theme.Header},
	kinds.DaemonSet:   {"DaemonSet", theme.Header},
	kinds.CronJob:     {"CronJob", theme.Header},
	kinds.Job:         {"job", theme.Accent},
	kinds.ReplicaSet:  {"rs", theme.Accent},
	kinds.Pod:         {"pod", theme.Body},
}

// rootOrder is the order roots appear under the Namespace node.
var rootOrder = []kinds.Kind{
	kinds.Deployment,
	kinds.StatefulSet,
	kinds.DaemonSet,
	kinds.CronJob,
	kinds.Job,
	kinds.ReplicaSet,
	kinds.Pod,
}

// collector accumulates the indexed resources as the walk adds nodes.
type collector struct {
	indexed   bool
	resources []Resource
}

// add appends a labelled child, numbering it and recording it when the tree is
// being indexed.
func (c *collector) add(parent *render.Node, style, prefix, name string, kind kinds.Kind) *render.Node {
	label := prefix + "/" + name
	if !c.indexed {
		return parent.Add(label, style)
	}
	c.resources = append(c.resources, Resource{Name: name, Kind: kind})
	return parent.AddIndexed(label, style, len(c.resources))
}

func ownedBy(meta metav1.ObjectMeta, uid types.UID) bool {
	for _, ref := range meta.OwnerReferences {
		if ref.UID == uid {
			return true
		}
	}
	return false
}

// BuildResource graphs a single resource's ownership tree.
func (b Builder) BuildResource(
	ctx context.Context, kind kinds.Kind, name, namespace string, indexed bool,
) (*render.Node, []Resource, error) {
	c := &collector{indexed: indexed}

	root := &render.Node{Label: string(kind) + "/" + name, Style: theme.Header}
	if indexed {
		// The root is itself indexable, and numbers first.
		c.resources = append(c.resources, Resource{Name: name, Kind: kind})
		root.Index = 1
	}

	// Pods are listed once and filtered client-side, mirroring how the
	// namespace forest works: one call rather than one per owner.
	pods, err := b.pods(ctx, namespace)
	if err != nil {
		return nil, nil, err
	}

	switch kind {
	case kinds.Deployment:
		err = b.treeDeployment(ctx, name, namespace, root, pods, c)
	case kinds.ReplicaSet:
		err = b.treeOwnedPods(ctx, kinds.ReplicaSet, name, namespace, root, pods, c)
	case kinds.StatefulSet:
		err = b.treeOwnedPods(ctx, kinds.StatefulSet, name, namespace, root, pods, c)
	case kinds.DaemonSet:
		err = b.treeOwnedPods(ctx, kinds.DaemonSet, name, namespace, root, pods, c)
	case kinds.CronJob:
		err = b.treeCronJob(ctx, name, namespace, root, pods, c)
	case kinds.Service:
		err = b.treeService(ctx, name, namespace, root, c)
	case kinds.Pod:
		pod, podErr := b.Client.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
		if podErr != nil {
			return nil, nil, podErr
		}
		addContainers(pod, root)
	default:
		root.Add("(no ownership graph for "+string(kind)+")", theme.Muted)
	}
	if err != nil {
		return nil, nil, err
	}
	return root, c.resources, nil
}

func (b Builder) pods(ctx context.Context, namespace string) ([]corev1.Pod, error) {
	list, err := b.Client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (b Builder) treeDeployment(
	ctx context.Context, name, namespace string, node *render.Node,
	pods []corev1.Pod, c *collector,
) error {
	deployment, err := b.Client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	replicaSets, err := b.Client.AppsV1().ReplicaSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	// A Deployment mid-rollout owns several ReplicaSets; all of them are shown
	// so surge and old pods are visible rather than silently dropped.
	for i := range replicaSets.Items {
		rs := &replicaSets.Items[i]
		if !ownedBy(rs.ObjectMeta, deployment.UID) {
			continue
		}
		rsNode := c.add(node, theme.Accent, "rs", rs.Name, kinds.ReplicaSet)
		addPodsForOwner(rs.UID, pods, rsNode, c)
	}
	return nil
}

// treeOwnedPods handles the kinds that own their pods directly.
func (b Builder) treeOwnedPods(
	ctx context.Context, kind kinds.Kind, name, namespace string,
	node *render.Node, pods []corev1.Pod, c *collector,
) error {
	uid, err := b.workloadUID(ctx, kind, name, namespace)
	if err != nil {
		return err
	}
	addPodsForOwner(uid, pods, node, c)
	return nil
}

func (b Builder) workloadUID(
	ctx context.Context, kind kinds.Kind, name, namespace string,
) (types.UID, error) {
	get := metav1.GetOptions{}
	switch kind {
	case kinds.ReplicaSet:
		object, err := b.Client.AppsV1().ReplicaSets(namespace).Get(ctx, name, get)
		if err != nil {
			return "", err
		}
		return object.UID, nil
	case kinds.StatefulSet:
		object, err := b.Client.AppsV1().StatefulSets(namespace).Get(ctx, name, get)
		if err != nil {
			return "", err
		}
		return object.UID, nil
	case kinds.DaemonSet:
		object, err := b.Client.AppsV1().DaemonSets(namespace).Get(ctx, name, get)
		if err != nil {
			return "", err
		}
		return object.UID, nil
	case kinds.Job:
		object, err := b.Client.BatchV1().Jobs(namespace).Get(ctx, name, get)
		if err != nil {
			return "", err
		}
		return object.UID, nil
	default:
		return "", nil
	}
}

func (b Builder) treeCronJob(
	ctx context.Context, name, namespace string, node *render.Node,
	pods []corev1.Pod, c *collector,
) error {
	cronJob, err := b.Client.BatchV1().CronJobs(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	jobs, err := b.Client.BatchV1().Jobs(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for i := range jobs.Items {
		job := &jobs.Items[i]
		if !ownedBy(job.ObjectMeta, cronJob.UID) {
			continue
		}
		jobNode := c.add(node, theme.Accent, "job", job.Name, kinds.Job)
		addPodsForOwner(job.UID, pods, jobNode, c)
	}
	return nil
}

func (b Builder) treeService(
	ctx context.Context, name, namespace string, node *render.Node, c *collector,
) error {
	service, err := b.Client.CoreV1().Services(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if len(service.Spec.Selector) == 0 {
		// A headless or externalName Service selects nothing; say so rather
		// than rendering an empty branch.
		node.Add("(no selector)", theme.Muted)
		return nil
	}
	list, err := b.Client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: formatSelector(service.Spec.Selector),
	})
	if err != nil {
		return err
	}
	if len(list.Items) == 0 {
		node.Add("(no matching pods)", theme.Muted)
		return nil
	}
	for i := range list.Items {
		pod := &list.Items[i]
		podNode := c.add(node, theme.Body, "pod", pod.Name, kinds.Pod)
		addContainers(pod, podNode)
	}
	return nil
}

func addPodsForOwner(uid types.UID, pods []corev1.Pod, parent *render.Node, c *collector) {
	for i := range pods {
		pod := &pods[i]
		if !ownedBy(pod.ObjectMeta, uid) {
			continue
		}
		podNode := c.add(parent, theme.Body, "pod", pod.Name, kinds.Pod)
		addContainers(pod, podNode)
	}
}

func addContainers(pod *corev1.Pod, parent *render.Node) {
	for _, container := range pod.Spec.Containers {
		parent.Add("container: "+container.Name, theme.Muted)
	}
}

// MostRecentJob returns the most recently created Job owned by uid (a CronJob's
// uid), or nil if it has never run.
//
// CronJob health and pods are both scoped to the latest run rather than the
// full retained history, so this is shared with the diagnostics.
func MostRecentJob(uid types.UID, jobs []batchv1.Job) *batchv1.Job {
	var recent *batchv1.Job
	for i := range jobs {
		job := &jobs[i]
		if !ownedBy(job.ObjectMeta, uid) {
			continue
		}
		if recent == nil || job.CreationTimestamp.Time.After(recent.CreationTimestamp.Time) {
			recent = job
		}
	}
	return recent
}

// ownerRef is one entry in the namespace forest's owner index.
type ownerRef struct {
	object metav1.Object
	kind   kinds.Kind
	// containers is set for pods, which render their containers as leaves.
	pod *corev1.Pod
}

// BuildNamespace graphs the whole ownership forest for a namespace: every
// workload controller as a root with its owned resources beneath, plus orphaned
// Jobs, ReplicaSets and bare Pods so nothing is hidden. Each pod appears
// exactly once.
//
// Unlike BuildResource, the Namespace root is not indexed — children number
// from 1.
func (b Builder) BuildNamespace(
	ctx context.Context, namespace string, indexed bool,
) (*render.Node, []Resource, error) {
	deployments, err := b.Client.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, nil, err
	}
	replicaSets, err := b.Client.AppsV1().ReplicaSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, nil, err
	}
	statefulSets, err := b.Client.AppsV1().StatefulSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, nil, err
	}
	daemonSets, err := b.Client.AppsV1().DaemonSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, nil, err
	}
	cronJobs, err := b.Client.BatchV1().CronJobs(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, nil, err
	}
	jobs, err := b.Client.BatchV1().Jobs(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, nil, err
	}
	pods, err := b.Client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, nil, err
	}

	// Resources that something else in the namespace can own.
	var children []ownerRef
	for i := range replicaSets.Items {
		children = append(children, ownerRef{object: &replicaSets.Items[i], kind: kinds.ReplicaSet})
	}
	for i := range jobs.Items {
		children = append(children, ownerRef{object: &jobs.Items[i], kind: kinds.Job})
	}
	for i := range pods.Items {
		children = append(children, ownerRef{
			object: &pods.Items[i], kind: kinds.Pod, pod: &pods.Items[i],
		})
	}

	// Indexed by owner uid so a parent finds its children in one pass.
	childrenByOwner := map[types.UID][]ownerRef{}
	for _, child := range children {
		for _, ref := range child.object.GetOwnerReferences() {
			childrenByOwner[ref.UID] = append(childrenByOwner[ref.UID], child)
		}
	}

	present := map[types.UID]bool{}
	markPresent := func(uid types.UID) { present[uid] = true }
	for i := range deployments.Items {
		markPresent(deployments.Items[i].UID)
	}
	for i := range replicaSets.Items {
		markPresent(replicaSets.Items[i].UID)
	}
	for i := range statefulSets.Items {
		markPresent(statefulSets.Items[i].UID)
	}
	for i := range daemonSets.Items {
		markPresent(daemonSets.Items[i].UID)
	}
	for i := range cronJobs.Items {
		markPresent(cronJobs.Items[i].UID)
	}
	for i := range jobs.Items {
		markPresent(jobs.Items[i].UID)
	}
	for i := range pods.Items {
		markPresent(pods.Items[i].UID)
	}

	// Controllers are always roots. An owned child is a root only when
	// orphaned — its owner isn't among the namespace's collected objects —
	// which is what keeps every pod visible exactly once.
	var roots []ownerRef
	for i := range deployments.Items {
		roots = append(roots, ownerRef{object: &deployments.Items[i], kind: kinds.Deployment})
	}
	for i := range statefulSets.Items {
		roots = append(roots, ownerRef{object: &statefulSets.Items[i], kind: kinds.StatefulSet})
	}
	for i := range daemonSets.Items {
		roots = append(roots, ownerRef{object: &daemonSets.Items[i], kind: kinds.DaemonSet})
	}
	for i := range cronJobs.Items {
		roots = append(roots, ownerRef{object: &cronJobs.Items[i], kind: kinds.CronJob})
	}
	for _, child := range children {
		owned := false
		for _, ref := range child.object.GetOwnerReferences() {
			if present[ref.UID] {
				owned = true
				break
			}
		}
		if !owned {
			roots = append(roots, child)
		}
	}

	order := map[kinds.Kind]int{}
	for position, kind := range rootOrder {
		order[kind] = position
	}
	sortRoots(roots, order)

	c := &collector{indexed: indexed}
	root := &render.Node{Label: "Namespace/" + namespace, Style: theme.Header}
	if len(roots) == 0 {
		root.Add("(no workloads)", theme.Muted)
		return root, c.resources, nil
	}
	for _, entry := range roots {
		renderNode(entry, root, childrenByOwner, c)
	}
	return root, c.resources, nil
}

// renderNode adds an object under parent, then recurses into what it owns. A
// Pod is a leaf whose containers are listed directly.
func renderNode(entry ownerRef, parent *render.Node, childrenByOwner map[types.UID][]ownerRef, c *collector) {
	style := nodeStyle[entry.kind]
	node := c.add(parent, style.Style, style.Prefix, entry.object.GetName(), entry.kind)
	if entry.kind == kinds.Pod {
		addContainers(entry.pod, node)
		return
	}
	for _, child := range childrenByOwner[entry.object.GetUID()] {
		renderNode(child, node, childrenByOwner, c)
	}
}
