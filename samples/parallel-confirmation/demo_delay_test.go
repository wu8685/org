package parallelconfirmation

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/wu8685/org/sdk/orgsdk"
)

func TestRandomDemoDelayStaysWithinTwoToFiveSeconds(t *testing.T) {
	for range 64 {
		delay, err := randomDemoDelay("build-plan")
		if err != nil {
			t.Fatal(err)
		}
		if delay < 2*time.Second || delay > 5*time.Second {
			t.Fatalf("delay = %s", delay)
		}
	}
}

func TestEachExecutedActivityUsesAnIndependentInjectableDemoDelay(t *testing.T) {
	delays := map[string]time.Duration{
		"build-plan":               2 * time.Second,
		"execute-branch/summary":   3 * time.Second,
		"execute-branch/readiness": 4 * time.Second,
		"finalize":                 5 * time.Second,
	}
	var mu sync.Mutex
	requested := map[string]int{}
	slept := make([]time.Duration, 0, 4)
	worker, err := NewWorker("v1",
		withDemoDelaySource(func(activity string) (time.Duration, error) {
			mu.Lock()
			defer mu.Unlock()
			requested[activity]++
			return delays[activity], nil
		}),
		withDemoActivitySleeper(func(_ context.Context, delay time.Duration) error {
			mu.Lock()
			defer mu.Unlock()
			slept = append(slept, delay)
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	env := orgsdk.NewTestEnvironment()
	if err := env.Register(worker.Registrations()...); err != nil {
		t.Fatal(err)
	}
	approvalID := orgsdk.RuntimeNodeID(worker.Definition.Name, "", "approval-gate", "singleton")
	env.After(time.Second, func() {
		mu.Lock()
		defer mu.Unlock()
		if len(requested) != 0 || len(slept) != 0 {
			t.Errorf("idle approval wait invoked demo delay: requested=%v slept=%v", requested, slept)
		}
		env.SignalAction(orgsdk.ActionEnvelope{OperationID: "op-confirm", NodeID: approvalID, Action: "confirm", Input: []byte(`{}`)})
	})
	env.ExecuteWorkflow(WorkflowName, Input{Subject: "release notes"})
	if err := env.WorkflowError(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	for activity, delay := range delays {
		if requested[activity] != 1 {
			t.Errorf("delay requests for %s = %d", activity, requested[activity])
		}
		if delay < 2*time.Second || delay > 5*time.Second {
			t.Errorf("test delay outside contract: %s", delay)
		}
	}
	if len(requested) != 4 || len(slept) != 4 || delays["execute-branch/summary"] == delays["execute-branch/readiness"] {
		t.Fatalf("independent delays: requested=%v slept=%v", requested, slept)
	}
}

func TestDemoActivitySleepStopsPromptlyOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	if err := sleepDemoActivity(ctx, 5*time.Second); err != context.Canceled {
		t.Fatalf("sleep error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("canceled sleep took %s", elapsed)
	}
}
