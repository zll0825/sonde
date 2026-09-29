package pluginrunner

import (
	"context"
	"testing"
)

func TestForcedCollectionMarker(t *testing.T) {
	ctx := context.Background()
	if IsForcedCollection(ctx) {
		t.Fatal("plain context must not be forced")
	}
	if !IsForcedCollection(WithForcedCollection(ctx)) {
		t.Fatal("marked context must be forced")
	}
}
