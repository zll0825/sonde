package pluginrunner

import "context"

type forcedCollectionKey struct{}

// WithForcedCollection marks ctx as an explicit collection request (a Sync
// command from Core). Providers that throttle polling by publication
// frequency must fetch every series when this is set.
func WithForcedCollection(ctx context.Context) context.Context {
	return context.WithValue(ctx, forcedCollectionKey{}, true)
}

// IsForcedCollection reports whether ctx was marked by WithForcedCollection.
func IsForcedCollection(ctx context.Context) bool {
	forced, _ := ctx.Value(forcedCollectionKey{}).(bool)
	return forced
}
