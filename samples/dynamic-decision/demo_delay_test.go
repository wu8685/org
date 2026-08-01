package dynamicdecision

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/wu8685/org/sdk/orgsdk"
)

func TestRandomDemoDelayStaysWithinTwoToFiveSeconds(t *testing.T) {
	for range 64 {
		delay, err := randomDemoDelay("determine-route")
		if err != nil {
			t.Fatal(err)
		}
		if delay < 2*time.Second || delay > 5*time.Second {
			t.Fatalf("delay = %s", delay)
		}
	}
}

func TestOnlyExecutedDynamicActivitiesUseDemoDelay(t *testing.T) {
	for _, test := range []struct {
		mode, selected, skipped string
	}{
		{mode: "concise", selected: "concise-branch", skipped: "detailed-branch"},
		{mode: "detailed", selected: "detailed-branch", skipped: "concise-branch"},
	} {
		t.Run(test.mode, func(t *testing.T) {
			var mu sync.Mutex
			requested := map[string]int{}
			worker, err := NewWorker("v1",
				withDemoDelaySource(func(activity string) (time.Duration, error) {
					mu.Lock()
					defer mu.Unlock()
					requested[activity]++
					return time.Duration(2+len(requested)-1) * time.Second, nil
				}),
				withDemoActivitySleeper(func(context.Context, time.Duration) error { return nil }),
			)
			if err != nil {
				t.Fatal(err)
			}
			env := orgsdk.NewTestEnvironment()
			if err := env.Register(worker.Registrations()...); err != nil {
				t.Fatal(err)
			}
			env.ExecuteWorkflow(WorkflowName, Input{Mode: test.mode, Subject: "release notes"})
			if err := env.WorkflowError(); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			for _, activity := range []string{"determine-route", test.selected, "finalize"} {
				if requested[activity] != 1 {
					t.Errorf("delay requests for %s = %d", activity, requested[activity])
				}
			}
			if requested[test.skipped] != 0 || worker.Calls(test.skipped) != 0 || len(requested) != 3 {
				t.Fatalf("skipped branch delayed or executed: requested=%v calls=%d", requested, worker.Calls(test.skipped))
			}
			projection, err := env.Projection()
			if err != nil {
				t.Fatal(err)
			}
			if nodeByTemplate(projection, test.skipped).Status != orgsdk.NodeStatusSkipped {
				t.Fatalf("skipped projection = %#v", projection)
			}
		})
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
