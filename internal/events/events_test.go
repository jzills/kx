package events

import (
	"context"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

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

// dated builds an event last seen at a moment.
func dated(name string, at time.Time) corev1.Event {
	e := event(name, "Pod", "Failed")
	e.LastTimestamp = metav1.NewTime(at)
	return e
}

func TestWithinDropsWhatHappenedBeforeTheCutoff(t *testing.T) {
	now := time.Now()
	all := []corev1.Event{
		dated("recent", now.Add(-10*time.Minute)),
		dated("old", now.Add(-48*time.Hour)),
	}
	kept := Within(all, now.Add(-time.Hour))
	if len(kept) != 1 {
		t.Fatalf("kept %d events, want 1: %v", len(kept), kept)
	}
	if kept[0].InvolvedObject.Name != "recent" {
		t.Errorf("kept %q, want the one inside the window", kept[0].InvolvedObject.Name)
	}
}

// A zero cutoff is how "no window" is spelled, and it must keep everything —
// otherwise an unset events_max_age would hide every event kx used to list.
func TestWithinKeepsEverythingWithoutACutoff(t *testing.T) {
	all := []corev1.Event{
		dated("recent", time.Now()),
		dated("ancient", time.Now().Add(-365*24*time.Hour)),
	}
	if kept := Within(all, time.Time{}); len(kept) != 2 {
		t.Errorf("kept %d events, want both", len(kept))
	}
}

// Undated is never stale — the rule the diagnostics window already runs on.
// Events recorded through the newer API carry no LastTimestamp, and hiding one
// over a missing timestamp is the worse error.
func TestWithinKeepsAnUndatedEvent(t *testing.T) {
	undated := event("mystery", "Pod", "Failed")
	kept := Within([]corev1.Event{undated}, time.Now().Add(-time.Hour))
	if len(kept) != 1 {
		t.Errorf("kept %d events, want the undated one kept", len(kept))
	}
}

// An event dated only by its creation is dated all the same: Timestamp already
// falls back to it, and Within has to read the window through the same lens the
// AGE column does or a row can be dropped for a time it never displayed.
func TestWithinDatesAnEventTheWayTheTableDoes(t *testing.T) {
	created := event("created", "Pod", "Failed")
	created.CreationTimestamp = metav1.NewTime(time.Now().Add(-48 * time.Hour))
	if kept := Within([]corev1.Event{created}, time.Now().Add(-time.Hour)); len(kept) != 0 {
		t.Errorf("kept %d events, want the creation-dated one dropped", len(kept))
	}
}

func TestCutoffIsZeroWithoutAWindow(t *testing.T) {
	if got := Cutoff(0); !got.IsZero() {
		t.Errorf("Cutoff(0) = %v, want the zero time", got)
	}
	if got := Cutoff(-time.Hour); !got.IsZero() {
		t.Errorf("Cutoff(-1h) = %v, want the zero time", got)
	}
}

func TestCutoffOpensTheWindowAWindowAgo(t *testing.T) {
	before := time.Now()
	got := Cutoff(time.Hour)
	if want := before.Add(-time.Hour); got.Before(want) || got.After(time.Now().Add(-time.Hour)) {
		t.Errorf("Cutoff(1h) = %v, want an hour before now", got)
	}
}

// FirstTimestamp does not fall back to CreationTimestamp the way Timestamp
// does. Timestamp's fallback answers "when did this last happen", and a
// creation time is a fair enough answer; a first timestamp guessed the same
// way would be used to compute a span, and a wrong span is worse than an
// absent one — it would claim a burst was spread out, or the reverse.
func TestFirstTimestampIsAbsentRatherThanGuessed(t *testing.T) {
	created := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	event := corev1.Event{
		ObjectMeta: metav1.ObjectMeta{CreationTimestamp: metav1.NewTime(created)},
	}
	if got := FirstTimestamp(event); !got.IsZero() {
		t.Errorf("FirstTimestamp = %v for an event with none, want the zero time", got)
	}
}

func TestFirstTimestampReadsTheAPIField(t *testing.T) {
	first := time.Date(2026, 8, 13, 1, 20, 0, 0, time.UTC)
	event := corev1.Event{FirstTimestamp: metav1.NewTime(first)}
	if got := FirstTimestamp(event); !got.Equal(first) {
		t.Errorf("FirstTimestamp = %v, want %v", got, first)
	}
}

// pagedClient answers event lists from pages in order, recording the options
// each request carried — the fake clientset ignores field selectors and
// paging, so what was asked for has to be read off the request itself.
func pagedClient(pages ...[]corev1.Event) (*fake.Clientset, *[]metav1.ListOptions) {
	client := fake.NewSimpleClientset()
	var requests []metav1.ListOptions
	client.PrependReactor("list", "events", func(action k8stesting.Action) (bool, runtime.Object, error) {
		options := action.(k8stesting.ListActionImpl).ListOptions
		requests = append(requests, options)
		page := len(requests) - 1
		list := &corev1.EventList{Items: pages[page]}
		if page+1 < len(pages) {
			list.Continue = fmt.Sprintf("page-%d", page+1)
		}
		return true, list, nil
	})
	return client, &requests
}

// The diagnostics read Warning events alone, so that is all they ask the API
// server for: filtered there, not after every event in the cluster arrived.
func TestWarningsAsksTheServerForWarningsOnly(t *testing.T) {
	client, requests := pagedClient([]corev1.Event{event("api", "Pod", "BackOff")})
	if _, err := (APIService{Client: client}).Warnings(context.Background(), ""); err != nil {
		t.Fatalf("Warnings: %v", err)
	}
	if got := (*requests)[0].FieldSelector; got != "type=Warning" {
		t.Errorf("FieldSelector = %q, want type=Warning", got)
	}
}

// kx events shows Normal events too, so Get asks for everything.
func TestGetAsksForEveryType(t *testing.T) {
	client, requests := pagedClient([]corev1.Event{event("api", "Pod", "Pulled")})
	if _, err := (APIService{Client: client}).Get(context.Background(), "prod"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := (*requests)[0].FieldSelector; got != "" {
		t.Errorf("FieldSelector = %q, want none", got)
	}
}

// Both read in bounded pages, following the continue token until the server
// stops giving one, and return every page's events in order.
func TestEventListingsFollowEveryPage(t *testing.T) {
	for name, read := range map[string]func(APIService) ([]corev1.Event, error){
		"Get":      func(s APIService) ([]corev1.Event, error) { return s.Get(context.Background(), "") },
		"Warnings": func(s APIService) ([]corev1.Event, error) { return s.Warnings(context.Background(), "") },
	} {
		client, requests := pagedClient(
			[]corev1.Event{event("a", "Pod", "BackOff")},
			[]corev1.Event{event("b", "Pod", "BackOff")},
			[]corev1.Event{event("c", "Pod", "BackOff")},
		)
		got, err := read(APIService{Client: client})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(got) != 3 || got[0].InvolvedObject.Name != "a" || got[2].InvolvedObject.Name != "c" {
			t.Errorf("%s returned %d events, want a, b, c in order", name, len(got))
		}
		if len(*requests) != 3 {
			t.Fatalf("%s made %d requests, want 3", name, len(*requests))
		}
		for i, options := range *requests {
			if options.Limit != eventPageSize {
				t.Errorf("%s request %d Limit = %d, want %d", name, i, options.Limit, eventPageSize)
			}
			if want := map[int]string{0: "", 1: "page-1", 2: "page-2"}[i]; options.Continue != want {
				t.Errorf("%s request %d Continue = %q, want %q", name, i, options.Continue, want)
			}
		}
	}
}

// A continue token lapses once the version it was issued at is compacted, and
// the next page then fails with 410 Gone. The paging loop returned that error,
// failing a whole kx diag sweep, where the single list before paging could not
// meet it (#438). The read falls back to one full list instead, as client-go's
// pager does — still asking for Warnings alone, and without the events of the
// page it had already read appearing twice.
func TestAnExpiredContinueTokenFallsBackToAFullList(t *testing.T) {
	client := fake.NewSimpleClientset()
	var requests []metav1.ListOptions
	client.PrependReactor("list", "events", func(action k8stesting.Action) (bool, runtime.Object, error) {
		options := action.(k8stesting.ListActionImpl).ListOptions
		requests = append(requests, options)
		switch {
		case options.Continue == "page-1":
			return true, nil, apierrors.NewResourceExpired("the continue token has expired")
		case options.Limit == 0:
			return true, &corev1.EventList{Items: []corev1.Event{
				event("a", "Pod", "BackOff"), event("b", "Pod", "BackOff"), event("c", "Pod", "BackOff"),
			}}, nil
		}
		return true, &corev1.EventList{
			ListMeta: metav1.ListMeta{Continue: "page-1"},
			Items:    []corev1.Event{event("a", "Pod", "BackOff")},
		}, nil
	})
	got, err := (APIService{Client: client}).Warnings(context.Background(), "")
	if err != nil {
		t.Fatalf("Warnings: %v", err)
	}
	if len(got) != 3 || got[0].InvolvedObject.Name != "a" || got[2].InvolvedObject.Name != "c" {
		t.Errorf("Warnings returned %d events, want a, b, c from the full list", len(got))
	}
	if len(requests) != 3 {
		t.Fatalf("made %d requests, want a page, the expired page, then a full list", len(requests))
	}
	if full := requests[2]; full.FieldSelector != "type=Warning" || full.Continue != "" {
		t.Errorf("full list asked with %+v, want Warnings and no continue token", full)
	}
}
