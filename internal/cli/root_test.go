package cli

import (
	"testing"

	"github.com/jzills/kx/internal/config"
)

// Every other test in this package builds Services literally, so nothing else
// would notice if the production wiring stopped stamping the kubeconfig context
// onto saved state: the field would simply be empty for every real user, and an
// empty context is the value that waives the cluster check entirely.
func TestNewServicesWiresTheStateContextHook(t *testing.T) {
	services := NewServices(config.Config{MaxHistory: 10})

	if services.State.Context == nil {
		t.Fatal("NewServices left State.Context nil — saved state would record no context")
	}
}
