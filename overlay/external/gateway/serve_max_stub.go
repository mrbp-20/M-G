//go:build !(gateway || gateway.max)

package gateway

import (
	"context"
	"errors"
)

// MaxAvailable reports whether this binary carries the MAX adapter.
const MaxAvailable = false

// ServeMax reports that the MAX adapter was left out of this build.
func ServeMax(context.Context, Options) error {
	return errors.New("gateway: MAX is not built in (rebuild with -tags gateway.max, or -tags gateway for every adapter)")
}
