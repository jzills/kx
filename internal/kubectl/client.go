package kubectl

import (
	"context"
	"encoding/json"
	"os/exec"
	"time"
)

// Client is the kubectl binary kx runs, as `kx --version` reports it.
type Client struct {
	// Path is where kubectl was found on PATH, empty when it was not.
	Path string
	// Version is kubectl's own version, e.g. v1.36.1, empty when kubectl was
	// found but could not report one.
	Version string
}

// clientTimeout bounds `kubectl version --client`, which never contacts the
// cluster and returns in well under a second. The bound exists for a PATH entry
// named kubectl that is something else, so --version cannot hang on it.
const clientTimeout = 5 * time.Second

// DetectClient finds the kubectl on PATH and asks it for its version, the way
// every other call kx makes finds it. Only `kubectl version --client` runs, so
// nothing is sent to a cluster and no kubeconfig needs to load.
func DetectClient() Client {
	path, err := exec.LookPath("kubectl")
	if err != nil {
		return Client{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), clientTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "version", "--client", "-o", "json").Output()
	if err != nil {
		return Client{Path: path}
	}
	return Client{Path: path, Version: clientVersion(out)}
}

// clientVersion reads the version out of `kubectl version --client -o json`,
// empty when the reply isn't that shape.
func clientVersion(out []byte) string {
	var reply struct {
		ClientVersion struct {
			GitVersion string `json:"gitVersion"`
		} `json:"clientVersion"`
	}
	if json.Unmarshal(out, &reply) != nil {
		return ""
	}
	return reply.ClientVersion.GitVersion
}
