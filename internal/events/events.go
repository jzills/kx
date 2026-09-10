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

// Cutoff is the instant a window of the given length opens, or the zero time
// when there is no window.
//
// Read once per run and passed down rather than recomputed per event, so every
// event in one listing is measured against the same moment — two events read
// milliseconds apart cannot fall on opposite sides of a cutoff that moved
// between them. Zero and negative both mean no window: nothing configures a
// negative one, and treating it as a window into the future would hide
// everything.
func Cutoff(window time.Duration) time.Time {
	if window <= 0 {
		return time.Time{}
	}
	return time.Now().Add(-window)
}

// Within narrows events to those at or after a cutoff, leaving them in the
// order they arrived. A zero cutoff is no window and keeps everything.
//
// An event the cluster never dated is kept, which is the same rule kx diag's
// window runs on: hiding a live signal over a missing timestamp is the worse
// error. Dated through Timestamp, so the window is read through the same lens
// the AGE column is rendered with — otherwise a row could be dropped for a
// time it never displayed.
func Within(events []corev1.Event, since time.Time) []corev1.Event {
	if since.IsZero() {
		return events
	}
	kept := make([]corev1.Event, 0, len(events))
	for _, event := range events {
		at := Timestamp(event)
		if at.IsZero() || !at.Before(since) {
			kept = append(kept, event)
		}
	}
	return kept
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
