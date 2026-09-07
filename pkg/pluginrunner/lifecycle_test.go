package pluginrunner

import (
	"context"
	"errors"
	"sonde/pkg/model"
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

// TestSecretReadiness_ReportsThisProcessEnvironment 锁住判断发生的位置。
//
// 密钥只存在于插件容器里。Core 与 API 各在自己的进程中，读不到这里的环境
// 变量——旧实现让 API 用 os.Getenv 判断，于是拿着真实密钥正常采集的插件
// 在首页上一直显示「缺少密钥」。判断必须在这一侧做出，再随心跳带走。
func TestSecretReadiness_ReportsThisProcessEnvironment(t *testing.T) {
	t.Setenv("SONDE_TEST_PRESENT_KEY", "set")
	t.Setenv("SONDE_TEST_MISSING_KEY", "")

	got := secretReadiness([]string{"SONDE_TEST_PRESENT_KEY", "SONDE_TEST_MISSING_KEY"})
	if got[model.SecretRuntimePrefix+"SONDE_TEST_PRESENT_KEY"] != model.SecretPresent {
		t.Errorf("present key = %q, want %q", got[model.SecretRuntimePrefix+"SONDE_TEST_PRESENT_KEY"], model.SecretPresent)
	}
	if got[model.SecretRuntimePrefix+"SONDE_TEST_MISSING_KEY"] != model.SecretMissing {
		t.Errorf("missing key = %q, want %q", got[model.SecretRuntimePrefix+"SONDE_TEST_MISSING_KEY"], model.SecretMissing)
	}
}

func TestSecretReadiness_NoDeclaredSecretsReportsNothing(t *testing.T) {
	// 不申报密钥的插件不该在 runtime 里留下任何 secret.* 键，否则 API 会
	// 为一个它并不需要的东西表态。
	if got := secretReadiness(nil); len(got) != 0 {
		t.Errorf("secretReadiness(nil) = %+v, want empty", got)
	}
}
