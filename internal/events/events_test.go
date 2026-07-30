package events

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/jzills/kx/internal/kinds"
)

func event(name, kind, reason string) corev1.Event {
	return corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Name: name + "." + reason, Namespace: "prod"},
		InvolvedObject: corev1.ObjectReference{Name: name, Kind: kind},
		Reason:         reason,
		Type:           "Warning",
		Message:        reason + " happened",
	}
}

func TestGetReadsNamespaceEvents(t *testing.T) {
	first := event("nginx", "Pod", "Failed")
	second := event("web", "Deployment", "ScalingReplicaSet")
	service := APIService{Client: fake.NewSimpleClientset(&first, &second)}

	all, err := service.Get(context.Background(), "prod")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("got %d events, want 2", len(all))
	}
}

// Both name and kind must match: a Pod and a Deployment can share a name.
func TestFilterMatchesNameAndKind(t *testing.T) {
	all := []corev1.Event{
		event("web", "Pod", "Failed"),
		event("web", "Deployment", "ScalingReplicaSet"),
		event("other", "Pod", "Killing"),
	}
	matched := APIService{}.Filter(all, "web", kinds.Pod)
	if len(matched) != 1 {
		t.Fatalf("got %d events, want 1: %v", len(matched), matched)
	}
	if matched[0].Reason != "Failed" {
		t.Errorf("matched the wrong event: %v", matched[0].Reason)
	}
}

func TestFilterNoMatches(t *testing.T) {
	all := []corev1.Event{event("web", "Pod", "Failed")}
	if matched := (APIService{}).Filter(all, "absent", kinds.Pod); len(matched) != 0 {
		t.Errorf("got %d events, want none", len(matched))
	}
}

// LastTimestamp is unset on events recorded through the newer events API, so
// without the fallback those events render with no age at all.
func TestTimestampFallsBackToCreation(t *testing.T) {
	created := time.Now().Add(-time.Hour)
	e := event("web", "Pod", "Failed")
	e.CreationTimestamp = metav1.NewTime(created)
	if got := Timestamp(e); !got.Equal(created) {
		t.Errorf("Timestamp = %v, want the creation time %v", got, created)
	}

	seen := time.Now().Add(-time.Minute)
	e.LastTimestamp = metav1.NewTime(seen)
	if got := Timestamp(e); !got.Equal(seen) {
		t.Errorf("Timestamp = %v, want the last-seen time %v", got, seen)
	}
}

// The API leaves Count at zero rather than one for a single occurrence.
func TestCountTreatsZeroAsOne(t *testing.T) {
	e := event("web", "Pod", "Failed")
	if got := Count(e); got != 1 {
		t.Errorf("Count = %d, want 1", got)
	}
	e.Count = 5
	if got := Count(e); got != 5 {
		t.Errorf("Count = %d, want 5", got)
	}
}

func TestRowsFlattensEvents(t *testing.T) {
	e := event("nginx", "Pod", "BackOff")
	e.LastTimestamp = metav1.NewTime(time.Now())
	rows := Rows([]corev1.Event{e})
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	row := rows[0]
	if row.Reason != "BackOff" || row.Kind != "Pod" || row.Type != "Warning" {
		t.Errorf("row = %+v", row)
	}
	if row.Timestamp.IsZero() {
		t.Error("row has no timestamp")
	}
}
