package pluginrunner

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	pb "sonde/pkg/proto/plugin/v1"
)

func TestRunSessionValidatesCollectorBeforeRegistration(t *testing.T) {
	setupErr := errors.New("missing provider credential")
	registrationBuilt := false
	lifecycle := NewLifecycle(Config{
		PluginName: "startup-order-test",
		Version:    "0.0.0",
		BuildRegistration: func() *pb.RegisterPluginRequest {
			registrationBuilt = true
			return &pb.RegisterPluginRequest{}
		},
		SetupCollector: func(context.Context) (Provider, error) {
			return nil, setupErr
		},
	})

	err := lifecycle.runSession(context.Background(), "127.0.0.1:1", time.Hour)
	if !errors.Is(err, setupErr) || !strings.Contains(err.Error(), "setup collector") {
		t.Fatalf("runSession error = %v, want wrapped setup error", err)
	}
	if registrationBuilt {
		t.Fatal("registration was built before collector startup validation")
	}
}
