package pluginrunner

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "capital_observatory/pkg/proto/plugin/v1"
	"google.golang.org/protobuf/proto"
)

func TestCommandLedgerConcurrentDeliveryExecutesOnce(t *testing.T) {
	ledger := newCommandLedger(16, time.Hour)
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	collect := func() ([]Snapshot, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return []Snapshot{{MetricID: "metric.once", Value: 42}}, nil
	}

	const deliveries = 8
	results := make(chan commandExecutionResult, deliveries)
	var wg sync.WaitGroup
	for i := 0; i < deliveries; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- ledger.execute("cmd_once", collect)
		}()
	}
	<-started
	close(release)
	wg.Wait()
	close(results)

	if calls.Load() != 1 {
		t.Fatalf("collector calls = %d, want 1", calls.Load())
	}
	for result := range results {
		if result.err != nil || len(result.snapshots) != 1 || result.snapshots[0].Value != 42 {
			t.Fatalf("replayed result = %+v, want shared successful snapshots", result)
		}
	}
}

func TestCommandLedgerSuccessfulRedeliveryReusesResult(t *testing.T) {
	ledger := newCommandLedger(16, time.Hour)
	now := time.Date(2026, 8, 7, 1, 2, 3, 0, time.UTC)
	ledger.now = func() time.Time { return now }
	calls := 0
	collect := func() ([]Snapshot, error) {
		calls++
		return []Snapshot{{MetricID: "metric.cached", Value: float64(calls)}}, nil
	}

	first := ledger.execute("cmd_cached", collect)
	second := ledger.execute("cmd_cached", collect)

	if calls != 1 {
		t.Fatalf("collector calls = %d, want 1", calls)
	}
	if first.snapshots[0].Value != second.snapshots[0].Value {
		t.Fatalf("results differ: first=%v second=%v", first.snapshots, second.snapshots)
	}
	if first.snapshots[0].FetchedAt != now || second.snapshots[0].FetchedAt != now {
		t.Fatalf("replayed fetched_at differs: first=%s second=%s", first.snapshots[0].FetchedAt, second.snapshots[0].FetchedAt)
	}
	if first.ackTimestamp != second.ackTimestamp || first.ackTimestamp != now.Unix() {
		t.Fatalf("replayed ack timestamp = %d/%d, want %d", first.ackTimestamp, second.ackTimestamp, now.Unix())
	}

	first.snapshots[0].Value = 99
	third := ledger.execute("cmd_cached", collect)
	if third.snapshots[0].Value != 1 {
		t.Fatalf("caller mutated cached snapshots: got %v, want 1", third.snapshots[0].Value)
	}
}

func TestSubmitAndAckReplaysExactWirePayloads(t *testing.T) {
	ledger := newCommandLedger(16, time.Hour)
	now := time.Date(2026, 8, 7, 1, 2, 3, 0, time.UTC)
	ledger.now = func() time.Time { return now }
	result := ledger.execute("cmd_wire", func() ([]Snapshot, error) {
		return []Snapshot{{MetricID: "metric.wire", Value: 42}}, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner := &Runner{
		version: "1.0.0",
		sendCh:  make(chan *pb.PluginMessage, 4),
		ctx:     ctx,
	}

	submitAndAck(runner, "plg_wire", "cmd_wire", result)
	firstPush, firstAck := <-runner.sendCh, <-runner.sendCh
	submitAndAck(runner, "plg_wire", "cmd_wire", result)
	secondPush, secondAck := <-runner.sendCh, <-runner.sendCh

	if !proto.Equal(firstPush, secondPush) {
		t.Fatalf("push replay differs: first=%v second=%v", firstPush, secondPush)
	}
	if !proto.Equal(firstAck, secondAck) {
		t.Fatalf("ack replay differs: first=%v second=%v", firstAck, secondAck)
	}
	if got := firstAck.GetCommandAck().GetTimestamp(); got != now.Unix() {
		t.Fatalf("ack timestamp = %d, want %d", got, now.Unix())
	}
}

func TestCommandLedgerFailureCanRetry(t *testing.T) {
	ledger := newCommandLedger(16, time.Hour)
	temporary := errors.New("temporary provider failure")
	calls := 0
	collect := func() ([]Snapshot, error) {
		calls++
		if calls == 1 {
			return nil, temporary
		}
		return []Snapshot{{MetricID: "metric.retry", Value: 1}}, nil
	}

	if result := ledger.execute("cmd_retry", collect); !errors.Is(result.err, temporary) {
		t.Fatalf("first result error = %v, want temporary", result.err)
	}
	if result := ledger.execute("cmd_retry", collect); result.err != nil || len(result.snapshots) != 1 {
		t.Fatalf("retry result = %+v, want success", result)
	}
	if calls != 2 {
		t.Fatalf("collector calls = %d, want 2", calls)
	}
}

func TestCommandLedgerEvictsByTTLAndCapacity(t *testing.T) {
	now := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)
	ledger := newCommandLedger(2, time.Hour)
	ledger.now = func() time.Time { return now }
	calls := 0
	collect := func() ([]Snapshot, error) {
		calls++
		return []Snapshot{{MetricID: "metric.eviction", Value: float64(calls)}}, nil
	}

	ledger.execute("cmd_a", collect)
	now = now.Add(time.Minute)
	ledger.execute("cmd_b", collect)
	now = now.Add(time.Minute)
	ledger.execute("cmd_c", collect) // capacity evicts cmd_a
	ledger.execute("cmd_a", collect)
	if calls != 4 {
		t.Fatalf("calls after capacity eviction = %d, want 4", calls)
	}

	now = now.Add(2 * time.Hour)
	ledger.execute("cmd_a", collect) // TTL evicts the prior cmd_a result
	if calls != 5 {
		t.Fatalf("calls after TTL eviction = %d, want 5", calls)
	}
}

func TestCommandLedgerCapacityBlocksDistinctInflightCommand(t *testing.T) {
	ledger := newCommandLedger(1, time.Hour)
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondStarted := make(chan struct{})
	done := make(chan struct{}, 2)

	go func() {
		ledger.execute("cmd_first", func() ([]Snapshot, error) {
			close(firstStarted)
			<-releaseFirst
			return []Snapshot{{MetricID: "first"}}, nil
		})
		done <- struct{}{}
	}()
	<-firstStarted

	go func() {
		ledger.execute("cmd_second", func() ([]Snapshot, error) {
			close(secondStarted)
			return []Snapshot{{MetricID: "second"}}, nil
		})
		done <- struct{}{}
	}()

	select {
	case <-secondStarted:
		t.Fatal("second distinct command started while the only ledger slot was in flight")
	case <-time.After(20 * time.Millisecond):
	}

	close(releaseFirst)
	for i := 0; i < 2; i++ {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("ledger execution did not finish")
		}
	}
	if len(ledger.entries) > ledger.capacity {
		t.Fatalf("ledger entries = %d, capacity = %d", len(ledger.entries), ledger.capacity)
	}
}
