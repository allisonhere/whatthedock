package clipboard

import (
	"testing"

	"github.com/allisonhere/whatthedock/internal/domain"
)

// A container started with --restart on-failure:5 must paste with its cap,
// not as a bare on-failure (which retries forever).
func TestYankPasteKeepsOnFailureRetryCap(t *testing.T) {
	ctr := domain.Container{Name: "worker", Image: "img", RestartPolicy: "on-failure", RestartMaxRetries: 5}

	pc := FromContainer(ctr, domain.Host{ID: "h", Name: "h"})
	if pc.RestartMaxRetries != 5 {
		t.Fatalf("PortableContainer.RestartMaxRetries = %d, want 5", pc.RestartMaxRetries)
	}
	spec := pc.ToCreateSpec()
	if spec.RestartPolicy != "on-failure" || spec.RestartMaxRetries != 5 {
		t.Fatalf("spec restart = %q:%d, want on-failure:5", spec.RestartPolicy, spec.RestartMaxRetries)
	}
}
