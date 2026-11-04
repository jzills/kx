// Package k8s builds the Kubernetes API client.
//
// kx delegates resource operations to kubectl; the API client exists only for
// the commands that need structured data kubectl's table output can't provide —
// the ownership graph and the diagnostics.
package k8s

import (
	"fmt"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Client builds a clientset from the ambient configuration: the caller's
// kubeconfig first, falling back to the in-cluster service account when kx runs
// inside a pod.
//
// Unlike the Python implementation, no CA bundle is shipped alongside: a Go
// binary reads the system trust store at runtime, so there is nothing for a
// bundled build to be missing.
func Client() (*kubernetes.Clientset, error) {
	config, err := restConfig()
	if err != nil {
		return nil, err
	}
	// client-go surfaces API deprecation warnings on stderr by default (the
	// Endpoints v1 notice, for one). kx reads Endpoints deliberately, and a
	// warning it can't act on would land in the middle of a diagnostic report.
	config.WarningHandler = rest.NoWarnings{}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("Could not build a Kubernetes client: %w.", err)
	}
	return clientset, nil
}

// Cluster is the cluster a kubectl command reaches, as kubectl's
// cluster-selecting flags choose it: --kubeconfig, --context, --cluster and
// --server. The zero value is the kubeconfig's own choice, which is what every
// command without those flags gets.
type Cluster struct {
	Kubeconfig string
	Context    string
	Cluster    string
	Server     string
}

// IsZero reports whether no flag chose a cluster.
func (c Cluster) IsZero() bool { return c == Cluster{} }

// clientConfig is the kubeconfig loaded as kubectl loads it, with c's
// overrides applied the way kubectl applies its own flags.
func clientConfig(c Cluster) clientcmd.ClientConfig {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	rules.ExplicitPath = c.Kubeconfig
	overrides := &clientcmd.ConfigOverrides{CurrentContext: c.Context}
	overrides.Context.Cluster = c.Cluster
	overrides.ClusterInfo.Server = c.Server
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides)
}

func restConfig() (*rest.Config, error) {
	config, err := clientConfig(Cluster{}).ClientConfig()
	if err == nil {
		return config, nil
	}

	inCluster, inClusterErr := rest.InClusterConfig()
	if inClusterErr != nil {
		// Report the kubeconfig failure: a user outside a cluster is far more
		// likely to have a kubeconfig problem than to have meant in-cluster.
		return nil, fmt.Errorf("Could not load the Kubernetes configuration: %w.", err)
	}
	return inCluster, nil
}

// Namespace reports the namespace the current kubeconfig context selects,
// falling back to "default".
func Namespace() string {
	return NamespaceFor(Cluster{})
}

// NamespaceFor is Namespace for the cluster c chooses: the default namespace
// of the context --context names, as kubectl would use it.
func NamespaceFor(c Cluster) string {
	namespace, _, err := clientConfig(c).Namespace()
	if err != nil || namespace == "" {
		return "default"
	}
	return namespace
}

// CurrentContext reports the kubeconfig's current context, or "" when none is
// set — a kubeconfig with no current context is a legitimate setup, not a
// failure to report.
//
// A local file read and YAML parse, same as Namespace above — no subprocess,
// unlike shelling out to `kubectl config current-context`. Uses the same
// loading rules (KUBECONFIG, the default file list) kubectl itself does, so
// the answer can't disagree with what a kubectl command run right after it
// would use.
func CurrentContext() (string, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	raw, err := rules.Load()
	if err != nil {
		return "", err
	}
	return raw.CurrentContext, nil
}
