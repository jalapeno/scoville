// Package noop provides a SouthboundDriver that performs no dataplane
// programming. It is used with EncapModeHost, where the host/GPU programs
// its own SRv6 state by pulling segment lists from the /flows endpoint.
package noop

import (
	"context"

	"github.com/jalapeno/scoville/internal/southbound"
)

// Driver is the no-op southbound driver. All operations succeed immediately.
type Driver struct{}

// New returns a new no-op driver.
func New() *Driver { return &Driver{} }

// ProgramWorkload is a no-op; the host is responsible for its own SRv6 state.
func (d *Driver) ProgramWorkload(_ context.Context, _ *southbound.ProgramRequest) error {
	return nil
}

// DeleteWorkload is a no-op.
func (d *Driver) DeleteWorkload(_ context.Context, _ string) error {
	return nil
}
