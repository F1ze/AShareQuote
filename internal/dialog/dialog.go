// Package dialog isolates native filesystem picker integration from HTTP.
package dialog

import "context"

func PickPath(ctx context.Context, kind string) (string, error) { return pickPath(ctx, kind) }
