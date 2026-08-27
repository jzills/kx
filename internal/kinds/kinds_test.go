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

type fakeShorthandSource struct {
	kind       Kind
	plural     string
	ok         bool
	calls      []string
	namespaced bool
	scopeKnown bool
	scopeCalls []Kind
}

func (f *fakeShorthandSource) Resolve(spelling string) (Kind, string, bool) {
	f.calls = append(f.calls, spelling)
	return f.kind, f.plural, f.ok
}

func (f *fakeShorthandSource) Namespaced(kind Kind) (bool, bool) {
	f.scopeCalls = append(f.scopeCalls, kind)
	return f.namespaced, f.scopeKnown
}

// A spelling already in kindMap must never reach the fallback — the static
// path is unchanged, byte-for-byte, regardless of what source is installed.
func TestNormalizeDoesNotConsultFallbackForKnownSpellings(t *testing.T) {
	fake := &fakeShorthandSource{}
	SetShorthandSource(fake)
	defer SetShorthandSource(nil)

	if got := Normalize("po"); got != Pod {
		t.Errorf("Normalize(po) = %q, want Pod", got)
	}
	if len(fake.calls) != 0 {
		t.Errorf("fallback was consulted for a known spelling: %v", fake.calls)
	}
}

func TestNormalizeConsultsFallbackOnMiss(t *testing.T) {
	fake := &fakeShorthandSource{kind: Kind("Gateway"), plural: "Gateways", ok: true}
	SetShorthandSource(fake)
	defer SetShorthandSource(nil)

	if got := Normalize("gw"); got != Kind("Gateway") {
		t.Errorf("Normalize(gw) = %q, want Gateway", got)
	}
	if len(fake.calls) != 1 || fake.calls[0] != "gw" {
		t.Errorf("calls = %v, want [gw]", fake.calls)
	}
}

func TestNormalizeFallsThroughWhenSourceMisses(t *testing.T) {
	fake := &fakeShorthandSource{ok: false}
	SetShorthandSource(fake)
	defer SetShorthandSource(nil)

	if got := Normalize("nonexistent"); got != Kind("nonexistent") {
		t.Errorf("Normalize(nonexistent) = %q, want the raw string passed through", got)
	}
}

func TestNormalizeWithNilSourceMatchesTodaysBehavior(t *testing.T) {
	SetShorthandSource(nil)
	if got := Normalize("gw"); got != Kind("gw") {
		t.Errorf("Normalize(gw) = %q, want the raw string passed through with no source installed", got)
	}
}

func TestIsKindSpellingConsultsFallback(t *testing.T) {
	SetShorthandSource(&fakeShorthandSource{ok: true})
	defer SetShorthandSource(nil)
	if !IsKindSpelling("gw") {
		t.Error("IsKindSpelling(gw) = false, want true when the fallback recognizes it")
	}

	SetShorthandSource(&fakeShorthandSource{ok: false})
	if IsKindSpelling("nonexistent") {
		t.Error("IsKindSpelling(nonexistent) = true, want false when nothing recognizes it")
	}
}

func TestPluralDisplayUsesFallbackPlural(t *testing.T) {
	SetShorthandSource(&fakeShorthandSource{kind: Kind("Gateway"), plural: "Gateways", ok: true})
	defer SetShorthandSource(nil)
	if got := PluralDisplay("gw"); got != "Gateways" {
		t.Errorf("PluralDisplay(gw) = %q, want Gateways", got)
	}
}

// A fallback that resolves the kind but reports no plural text still captions
// with the kind. It used to pass the spelling through instead, on the grounds
// that this matched every other unrecognized spelling — but an unrecognized
// spelling is one kx knows nothing about, and this one it has a kind for. The
// caption said "gw · prod · 3 items", echoing the shorthand the user typed
// back at them rather than naming what they were looking at.
//
// Defensive either way: discovery records a kind and its plural together, so a
// kind without one is a shape the real source does not produce.
func TestPluralDisplayNamesTheKindWithoutAPlural(t *testing.T) {
	SetShorthandSource(&fakeShorthandSource{kind: Kind("Gateway"), plural: "", ok: true})
	defer SetShorthandSource(nil)
	if got := PluralDisplay("gw"); got != "Gateways" {
		t.Errorf("PluralDisplay(gw) = %q, want the kind's plural", got)
	}
}

// Discovery reports a plural as the API resource *name* — a lowercase URL path
// segment, not a display string — so captioning with it directly printed
// "serviceaccounts · diagnostics · 1 item" beside "Pods" and "ConfigMaps".
//
// The kind supplies the register; the API supplies only the pluralising
// suffix, which is the part worth taking because the API server knows the
// irregulars.
func TestDisplayPluralKeepsTheKindsCase(t *testing.T) {
	for _, tc := range []struct {
		kind      Kind
		apiPlural string
		want      string
	}{
		// Plain "s".
		{"ServiceAccount", "serviceaccounts", "ServiceAccounts"},
		// "es", which a bare +"s" rule would have got wrong.
		{"Ingress", "ingresses", "Ingresses"},
		{"ComponentStatus", "componentstatuses", "ComponentStatuses"},
		// A kind that is already plural takes no suffix at all.
		{"Endpoints", "endpoints", "Endpoints"},
		// The stem changes, so there is no suffix to take.
		{"NetworkPolicy", "networkpolicies", "NetworkPolicies"},
		{"CSIStorageCapacity", "csistoragecapacities", "CSIStorageCapacities"},
		// Initialisms survive, because the kind is never rebuilt from the
		// lowercase name.
		{"APIService", "apiservices", "APIServices"},
		{"CSIDriver", "csidrivers", "CSIDrivers"},
		// Nothing to go on: still captioned in the kind's register rather than
		// a URL segment's.
		{"Gateway", "", "Gateways"},
	} {
		if got := displayPlural(tc.kind, tc.apiPlural); got != tc.want {
			t.Errorf("displayPlural(%q, %q) = %q, want %q", tc.kind, tc.apiPlural, got, tc.want)
		}
	}
}

// The whole point is what reaches a caption, so go through the exported entry
// point with a source installed, the way a real unknown type does.
func TestPluralDisplayCapitalisesADiscoveredType(t *testing.T) {
	fake := &fakeShorthandSource{kind: Kind("ServiceAccount"), plural: "serviceaccounts", ok: true}
	SetShorthandSource(fake)
	defer SetShorthandSource(nil)

	if got := PluralDisplay("sa"); got != "ServiceAccounts" {
		t.Errorf("PluralDisplay(sa) = %q, want ServiceAccounts", got)
	}
}

// Cluster-scoped kinds kx names itself must be recognised with no discovery
// cache at all — a fresh machine, or a kubeconfig kubectl has never populated
// a cache for. Without this, kx would caption a Node listing with whichever
// namespace the caller happened to be standing in.
func TestClusterScopedIsKnownWithoutADiscoverySource(t *testing.T) {
	SetShorthandSource(nil)
	for _, kind := range []Kind{Node, Namespace} {
		namespaced, known := Namespaced(kind)
		if !known {
			t.Errorf("Namespaced(%s) not known with no source installed", kind)
		}
		if namespaced {
			t.Errorf("Namespaced(%s) = true, want false — it is cluster-scoped", kind)
		}
	}
	for _, kind := range []Kind{Pod, Deployment, Secret, PersistentVolumeClaim} {
		namespaced, known := Namespaced(kind)
		if !known || !namespaced {
			t.Errorf("Namespaced(%s) = (%v, %v), want (true, true)", kind, namespaced, known)
		}
	}
}

// A kind kx has never heard of — a CRD — is answered by the discovery cache,
// which records scope per resource. There is no guessing fallback: an unknown
// kind with no source reports "not known", and the caller keeps today's
// behaviour rather than inventing a scope for it.
func TestNamespacedFallsBackToTheDiscoverySource(t *testing.T) {
	SetShorthandSource(nil)
	if _, known := Namespaced(Kind("Gateway")); known {
		t.Error("Namespaced(Gateway) claimed to know a CRD's scope with no source")
	}

	fake := &fakeShorthandSource{namespaced: false, scopeKnown: true}
	SetShorthandSource(fake)
	defer SetShorthandSource(nil)
	namespaced, known := Namespaced(Kind("ClusterIssuer"))
	if !known || namespaced {
		t.Errorf("Namespaced(ClusterIssuer) = (%v, %v), want (false, true)", namespaced, known)
	}
	if len(fake.scopeCalls) != 1 || fake.scopeCalls[0] != Kind("ClusterIssuer") {
		t.Errorf("scopeCalls = %v, want the kind asked about once", fake.scopeCalls)
	}
}

// The static table wins over the source, so a stale or wrong cache entry can
// never make kx treat a Pod as cluster-scoped.
func TestNamespacedPrefersTheStaticTable(t *testing.T) {
	SetShorthandSource(&fakeShorthandSource{namespaced: false, scopeKnown: true})
	defer SetShorthandSource(nil)
	if namespaced, known := Namespaced(Pod); !known || !namespaced {
		t.Errorf("Namespaced(Pod) = (%v, %v), want (true, true)", namespaced, known)
	}
}
