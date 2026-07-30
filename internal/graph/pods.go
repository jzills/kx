package graph

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/jzills/kx/internal/kinds"
)

// ResolveWorkloadPods returns the pods belonging to a workload, via ownership
// references.
//
// A Deployment is a two-hop walk (Deployment → owned ReplicaSets → owned pods,
// which includes surge and old pods mid-rollout); StatefulSet, DaemonSet and Job
// own their pods directly; a Pod resolves to itself; a Service matches by label
// selector rather than ownership. Pods are fetched once per namespace and
// filtered client-side, mirroring the tree builders.
func (b Builder) ResolveWorkloadPods(
	ctx context.Context, kind kinds.Kind, name, namespace string,
) ([]corev1.Pod, error) {
	switch kind {
	case kinds.Pod:
		pod, err := b.Client.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		return []corev1.Pod{*pod}, nil

	case kinds.Service:
		service, err := b.Client.CoreV1().Services(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		if len(service.Spec.Selector) == 0 {
			return nil, nil
		}
		list, err := b.Client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: formatSelector(service.Spec.Selector),
		})
		if err != nil {
			return nil, err
		}
		return list.Items, nil
	}

	pods, err := b.pods(ctx, namespace)
	if err != nil {
		return nil, err
	}

	switch kind {
	case kinds.Deployment:
		deployment, err := b.Client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		replicaSets, err := b.Client.AppsV1().ReplicaSets(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		owners := map[types.UID]bool{}
		for i := range replicaSets.Items {
			if ownedBy(replicaSets.Items[i].ObjectMeta, deployment.UID) {
				owners[replicaSets.Items[i].UID] = true
			}
		}
		return filterOwnedByAny(pods, owners), nil

	case kinds.StatefulSet, kinds.DaemonSet, kinds.Job:
		uid, err := b.workloadUID(ctx, kind, name, namespace)
		if err != nil {
			return nil, err
		}
		return filterOwnedByAny(pods, map[types.UID]bool{uid: true}), nil

	case kinds.CronJob:
		cronJob, err := b.Client.BatchV1().CronJobs(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		jobs, err := b.Client.BatchV1().Jobs(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		// Scoped to the latest run, not the full retained history.
		recent := MostRecentJob(cronJob.UID, jobs.Items)
		if recent == nil {
			return nil, nil
		}
		return filterOwnedByAny(pods, map[types.UID]bool{recent.UID: true}), nil

	default:
		return nil, nil
	}
}

func filterOwnedByAny(pods []corev1.Pod, owners map[types.UID]bool) []corev1.Pod {
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

// OwnedBy reports whether an object carries an owner reference to uid. Exported
// for the diagnostics sweep, which does its own ownership bookkeeping.
func OwnedBy(meta metav1.ObjectMeta, uid types.UID) bool { return ownedBy(meta, uid) }

// MatchesSelector reports whether a pod's labels satisfy every entry in a
// selector, which is how a Service claims its pods.
func MatchesSelector(pod corev1.Pod, selector map[string]string) bool {
	for key, value := range selector {
		if pod.Labels[key] != value {
			return false
		}
	}
	return true
}
