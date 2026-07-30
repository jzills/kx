package kinds

import (
	"strings"
	"testing"
)

func TestNormalizeShorthand(t *testing.T) {
	cases := map[string]Kind{
		"po":      Pod,
		"pods":    Pod,
		"deploy":  Deployment,
		"svc":     Service,
		"rs":      ReplicaSet,
		"sts":     StatefulSet,
		"ds":      DaemonSet,
		"cm":      ConfigMap,
		"pvc":     PersistentVolumeClaim,
		"ns":      Namespace,
		"hpa":     HorizontalPodAutoscaler,
		"cronjob": CronJob,
	}
	for shorthand, want := range cases {
		if got := Normalize(shorthand); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", shorthand, got, want)
		}
	}
}

func TestNormalizeIsCaseInsensitive(t *testing.T) {
	if got := Normalize("PODS"); got != Pod {
		t.Errorf("Normalize(\"PODS\") = %q, want Pod", got)
	}
}

// kx wraps kubectl, so a resource type it doesn't know must pass through rather
// than be rejected — CRDs are the common case.
func TestNormalizeUnknownPassesThrough(t *testing.T) {
	if got := Normalize("widgets.example.com"); got != Kind("widgets.example.com") {
		t.Errorf("Normalize(unknown) = %q, want it unchanged", got)
	}
}

func TestIsKindSpelling(t *testing.T) {
	if !IsKindSpelling("deploy") {
		t.Error("IsKindSpelling(\"deploy\") = false, want true")
	}
	if IsKindSpelling("widgets.example.com") {
		t.Error("IsKindSpelling(unknown) = true, want false")
	}
}

func TestPluralDisplay(t *testing.T) {
	cases := map[string]string{
		"pods":    "Pods",
		"deploy":  "Deployments",
		"ingress": "Ingresses",
		"svc":     "Services",
		"widgets": "widgets",
	}
	for input, want := range cases {
		if got := PluralDisplay(input); got != want {
			t.Errorf("PluralDisplay(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestEnsureKindAcceptsMatch(t *testing.T) {
	if err := EnsureKind(1, "nginx", Pod, Pod, nil); err != nil {
		t.Errorf("EnsureKind on a matching kind errored: %v", err)
	}
}

// The relist hint always names the canonical kind rather than whatever
// shorthand was typed — `kx get deployment`, never `kx get deploy`.
func TestEnsureKindMismatchNamesCanonicalKind(t *testing.T) {
	err := EnsureKind(3, "nginx-abc", Pod, Deployment, nil)
	if err == nil {
		t.Fatal("EnsureKind on a mismatch succeeded, want an error")
	}
	want := "Index 3 is Pod/nginx-abc, not Deployment — run 'kx get deployment' to relist."
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
}

type fakeLister struct{ has Kind }

func (f fakeLister) PreviousLists(kind Kind) bool { return kind == f.has }

// When the previous history entry lists the expected kind, `kx back` reaches it
// without re-running kubectl, so the error offers that too.
func TestEnsureKindOffersBackWhenPreviousListsKind(t *testing.T) {
	err := EnsureKind(3, "nginx-abc", Pod, Deployment, fakeLister{has: Deployment})
	if err == nil {
		t.Fatal("EnsureKind on a mismatch succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "or 'kx back' for the previous Deployment listing") {
		t.Errorf("error = %q, want it to offer 'kx back'", err)
	}
}

func TestEnsureKindOmitsBackWhenPreviousDoesNotList(t *testing.T) {
	err := EnsureKind(3, "nginx-abc", Pod, Deployment, fakeLister{has: Service})
	if err == nil {
		t.Fatal("EnsureKind on a mismatch succeeded, want an error")
	}
	if strings.Contains(err.Error(), "kx back") {
		t.Errorf("error = %q, want no 'kx back' hint", err)
	}
}
