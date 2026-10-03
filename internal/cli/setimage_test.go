package cli

import (
	"strings"
	"testing"

	"github.com/jzills/kx/internal/kinds"
)

const (
	oneContainerDeploy = `{"kind":"Deployment","spec":{"template":{"spec":{
		"containers":[{"name":"api","image":"api:v1"}]}}}}`
	sidecarDeploy = `{"kind":"Deployment","spec":{"template":{"spec":{
		"initContainers":[{"name":"migrate","image":"migrate:v1"}],
		"containers":[{"name":"api","image":"api:v1"},{"name":"envoy","image":"envoy:1.30"}]}}}}`
	cronJob = `{"kind":"CronJob","spec":{"jobTemplate":{"spec":{"template":{"spec":{
		"containers":[{"name":"report","image":"report:v1"}]}}}}}}`
)

// setImage runs kx set image over a one-row listing of kind, with the
// workload read back as object, and returns what it printed and ran.
func setImage(
	t *testing.T, kind kinds.Kind, object string, tagged bool, args ...string,
) (stdout, stderr string, kube *recordingKubectl, err error) {
	t.Helper()
	kube = &recordingKubectl{outputs: []string{object, ""}}
	services := switchServices(t, kube)
	saveListing(t, services, kind, "prod", tagged, "api")
	stdout, stderr, err = runCaptured(t, newSetCommand(services), append([]string{"image"}, args...))
	return stdout, stderr, kube, err
}

// setCalls is the kubectl set invocations among the recorded calls.
func setCalls(kube *recordingKubectl) []string {
	var calls []string
	for _, run := range kube.runs {
		if len(run) > 0 && run[0] == "set" {
			calls = append(calls, joinArgs(run))
		}
	}
	return calls
}

// A bare image names the workload's only container, and the line says what it
// replaced — the one record of what to type to put it back — followed by the
// way to follow the rollout it started.
func TestSetImageInfersTheOnlyContainer(t *testing.T) {
	stdout, _, kube, err := setImage(t, kinds.Deployment, oneContainerDeploy, false,
		"1", "api:v2")
	if err != nil {
		t.Fatalf("kx set image 1 api:v2: %v", err)
	}
	if got := setCalls(kube); len(got) != 1 || got[0] != "set image Deployment/api api=api:v2 -n prod" {
		t.Errorf("kubectl set calls = %q, want the one pair for api", got)
	}
	for _, want := range []string{
		"✓ Deployment/api · prod · api: api:v1 → api:v2",
		"kx rollout status 1 to follow it",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want %q", stdout, want)
		}
	}
}

// Init containers count: kubectl matches them by name too, so a bare image on
// a workload with any second container is a guess kx refuses to make — before
// anything is changed.
//
// The suggestion names the first container, not the first init container:
// it suggested 'migrate=api:v2', and copying it replaced the migration image
// (#434).
func TestSetImageRefusesABareImageWithSeveralContainers(t *testing.T) {
	_, _, kube, err := setImage(t, kinds.Deployment, sidecarDeploy, false, "1", "api:v2")
	if err == nil {
		t.Fatal("a bare image on three containers succeeded, want a refusal")
	}
	want := "Deployment/api has containers migrate, api, envoy — name one: " +
		"'kx set image 1 api=api:v2'."
	if err.Error() != want {
		t.Errorf("err = %q, want %q", err, want)
	}
	if got := setCalls(kube); len(got) != 0 {
		t.Errorf("kubectl set ran %q after the refusal", got)
	}
}

// Pairs reach kubectl verbatim, one line per container changed, and a pair
// that changes nothing says so rather than drawing an arrow to itself.
func TestSetImagePairsAndUnchangedImages(t *testing.T) {
	stdout, _, kube, err := setImage(t, kinds.Deployment, sidecarDeploy, false,
		"1", "api=api:v2", "envoy=envoy:1.30")
	if err != nil {
		t.Fatalf("kx set image with pairs: %v", err)
	}
	if got := setCalls(kube); len(got) != 1 ||
		got[0] != "set image Deployment/api api=api:v2 envoy=envoy:1.30 -n prod" {
		t.Errorf("kubectl set calls = %q", got)
	}
	for _, want := range []string{
		"Deployment/api · prod · api: api:v1 → api:v2",
		"Deployment/api · prod · envoy: already envoy:1.30",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want %q", stdout, want)
		}
	}
}

// * is kubectl's own spelling for every container, passed through as typed,
// and reported for each of them.
func TestSetImageStarSetsEveryContainer(t *testing.T) {
	stdout, _, kube, err := setImage(t, kinds.Deployment, sidecarDeploy, false, "1", "*=base:v9")
	if err != nil {
		t.Fatalf("kx set image 1 *=base:v9: %v", err)
	}
	if got := setCalls(kube); len(got) != 1 || !strings.Contains(got[0], " *=base:v9 ") {
		t.Errorf("kubectl set calls = %q, want *=base:v9 passed through", got)
	}
	if got := strings.Count(stdout, "→ base:v9"); got != 3 {
		t.Errorf("stdout = %q, want a line for each of the three containers", stdout)
	}
}

// Every refusal about what was typed happens before kubectl set image runs.
func TestSetImageRefusesBadSpecsBeforeChangingAnything(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"1", "sidecar=x:v1"}, "has no container named 'sidecar' — it has migrate, api, envoy"},
		{[]string{"1", "api:v2", "envoy=envoy:1.31"}, "a bare image can only stand alone"},
		{[]string{"1", "=api:v2"}, "is not a container=image pair"},
		{[]string{"1", "api="}, "is not a container=image pair"},
	} {
		_, _, kube, err := setImage(t, kinds.Deployment, sidecarDeploy, false, tc.args...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("kx set image %v: err = %v, want %q", tc.args, err, tc.want)
		}
		if got := setCalls(kube); len(got) != 0 {
			t.Errorf("kx set image %v: kubectl set ran %q after the refusal", tc.args, got)
		}
	}
}

// A Job's pod template is immutable, so it is refused before kx reads or
// changes anything.
func TestSetImageRefusesAJobWithoutCallingKubectl(t *testing.T) {
	_, _, kube, err := setImage(t, kinds.Job, oneContainerDeploy, false, "1", "api:v2")
	if err == nil || !strings.Contains(err.Error(), "kx set image does not support 'Job'") {
		t.Errorf("err = %v, want the unsupported-kind refusal", err)
	}
	if len(kube.runs) != 0 {
		t.Errorf("kubectl ran %q for a Job", kube.runs)
	}
}

// A CronJob keeps its pod spec a level deeper; the bare image still finds its
// one container, and with no rollout there is nothing to follow.
func TestSetImageOnACronJob(t *testing.T) {
	stdout, _, kube, err := setImage(t, kinds.CronJob, cronJob, false, "1", "report:v2")
	if err != nil {
		t.Fatalf("kx set image on a CronJob: %v", err)
	}
	if got := setCalls(kube); len(got) != 1 || got[0] != "set image CronJob/api report=report:v2 -n prod" {
		t.Errorf("kubectl set calls = %q", got)
	}
	if strings.Contains(stdout, "rollout status") {
		t.Errorf("stdout = %q, want no rollout hint for a CronJob", stdout)
	}
}

// kubectl's flags pass through after the images, and a dry run says nothing
// changed and points at no rollout, since none began. Every spelling kubectl
// reads as a dry run counts: a bare --dry-run is a client one, and it printed
// "api:v1 → api:v2" and a rollout to follow for a Deployment left untouched.
func TestSetImageDryRun(t *testing.T) {
	for _, flag := range []string{"--dry-run=server", "--dry-run", "--dry-run=true"} {
		stdout, _, kube, err := setImage(t, kinds.Deployment, oneContainerDeploy, false,
			"1", "api:v2", flag)
		if err != nil {
			t.Fatalf("kx set image %s: %v", flag, err)
		}
		if got := setCalls(kube); len(got) != 1 || !strings.HasSuffix(got[0], "-n prod "+flag) {
			t.Errorf("kubectl set calls = %q, want %s forwarded", got, flag)
		}
		if !strings.Contains(stdout, "(dry run — nothing was changed)") {
			t.Errorf("stdout = %q for %s, want the dry-run label", stdout, flag)
		}
		if strings.Contains(stdout, "rollout status") {
			t.Errorf("stdout = %q for %s, want no rollout hint on a dry run", stdout, flag)
		}
	}
}

// An index taken from an agent's listing is named before the change is made.
func TestSetImageNoticesAnAgentsListing(t *testing.T) {
	_, stderr, _, err := setImage(t, kinds.Deployment, oneContainerDeploy, true, "1", "api:v2")
	if err != nil {
		t.Fatalf("kx set image on an agent's listing: %v", err)
	}
	if got := strings.Count(stderr, noticeSubstring); got != 1 {
		t.Errorf("stderr = %q, want the agent notice exactly once", stderr)
	}
}

// Both halves are required, and a namespace flag beside an index is the same
// contradiction it is for every other index-taking command.
func TestSetImageArgumentShapes(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"1"}, "image"},
		{[]string{"1", "--dry-run=server"}, "image"},
		{[]string{"1", "api:v2", "-n", "staging"}, "cannot be combined with an index"},
	} {
		_, _, kube, err := setImage(t, kinds.Deployment, oneContainerDeploy, false, tc.args...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("kx set image %v: err = %v, want it to mention %q", tc.args, err, tc.want)
		}
		if got := setCalls(kube); len(got) != 0 {
			t.Errorf("kx set image %v: kubectl set ran %q", tc.args, got)
		}
	}
}

// Setting a container to the image it already runs starts no rollout, so
// there is nothing to point at.
func TestSetImageToTheSameImageHintsAtNoRollout(t *testing.T) {
	stdout, _, _, err := setImage(t, kinds.Deployment, oneContainerDeploy, false, "1", "api:v1")
	if err != nil {
		t.Fatalf("kx set image 1 api:v1: %v", err)
	}
	if !strings.Contains(stdout, "api: already api:v1") || strings.Contains(stdout, "rollout status") {
		t.Errorf("stdout = %q, want the unchanged line and no rollout hint", stdout)
	}
}

// kx set is a group with no RunE of its own, and cobra answers a non-root
// group's unknown subcommand with the group's help and a nil error — so
// `kx set env 1 FOO=bar` exited 0 having changed nothing, and a script took it
// for success. Driven through the root, because run on its own the group is a
// root, and cobra already refuses an unknown subcommand there.
func TestSetRefusesAnUnknownSubcommand(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		want string
	}{
		// One of kubectl's own set verbs is pointed at kx ref, the way to
		// spend an index on a verb kx doesn't wrap.
		{[]string{"set", "env", "1", "FOO=bar"}, "kx ref"},
		// A typo is offered the command it was probably meant to be.
		{[]string{"set", "imgae", "1", "nginx:2"}, "image"},
	} {
		quietRender(t)
		err := Execute(NewRoot(argvServices(t), "test"), tc.argv)
		if err == nil {
			t.Errorf("kx %v returned no error", tc.argv)
			continue
		}
		if !strings.Contains(err.Error(), `unknown command "`+tc.argv[1]+`" for "kx set"`) ||
			!strings.Contains(err.Error(), tc.want) {
			t.Errorf("kx %v: err = %q, want it refused as unknown, mentioning %q",
				tc.argv, err, tc.want)
		}
	}
}

// The group alone is still a request for its help, not a mistake.
func TestSetAloneShowsHelp(t *testing.T) {
	sink := captureRender(t)
	if err := Execute(NewRoot(argvServices(t), "test"), []string{"set"}); err != nil {
		t.Fatalf("kx set: %v", err)
	}
	if !strings.Contains(sink.String(), "kx set image") {
		t.Errorf("kx set printed %q, want its help", sink.String())
	}
}
