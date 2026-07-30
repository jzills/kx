// Package events reads Kubernetes events for a namespace and narrows them to a
// single object.
//
// Events come from the API server rather than kubectl because the diagnostics
// need them structured — reason, count and involved object — and re-parsing
// `kubectl get events` output would be brittle.
package events

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/jzills/kx/internal/kinds"
)

// Row is one event, flattened for rendering. Timestamp is the event's last-seen
// time, falling back to creation, or zero when unavailable.
type Row struct {
	Type      string
	Reason    string
	Kind      string
	Message   string
	Timestamp time.Time
}

// Service reads events from the API server.
type Service interface {
	Get(ctx context.Context, namespace string) ([]corev1.Event, error)
	Filter(events []corev1.Event, name string, kind kinds.Kind) []corev1.Event
}

// APIService is the real events service.
type APIService struct {
	Client kubernetes.Interface
}

func (s APIService) Get(ctx context.Context, namespace string) ([]corev1.Event, error) {
	list, err := s.Client.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

// Filter narrows events to one object, matched on the involved object's name
// and kind.
func (APIService) Filter(events []corev1.Event, name string, kind kinds.Kind) []corev1.Event {
	var matched []corev1.Event
	for _, event := range events {
		if event.InvolvedObject.Name == name && event.InvolvedObject.Kind == string(kind) {
			matched = append(matched, event)
		}
	}
	return matched
}

// Timestamp is an event's last-seen time, falling back to creation.
//
// LastTimestamp is unset on events recorded through the newer events API, so
// the fallback is not merely defensive — without it those events render with no
// age at all.
func Timestamp(event corev1.Event) time.Time {
	if !event.LastTimestamp.IsZero() {
		return event.LastTimestamp.Time
	}
	return event.CreationTimestamp.Time
}

// Count is the number of times an event fired. The API leaves it at zero rather
// than one for a single occurrence.
func Count(event corev1.Event) int32 {
	if event.Count == 0 {
		return 1
	}
	return event.Count
}

// Rows flattens events for rendering.
func Rows(events []corev1.Event) []Row {
	rows := make([]Row, 0, len(events))
	for _, event := range events {
		rows = append(rows, Row{
			Type:      event.Type,
			Reason:    event.Reason,
			Kind:      event.InvolvedObject.Kind,
			Message:   event.Message,
			Timestamp: Timestamp(event),
		})
	}
	return rows
}
