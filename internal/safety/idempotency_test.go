package safety

import (
	"context"
	"errors"
	"testing"
)

func TestWorkerCrashAfterExternalSuccessBeforeTemporalCompletionDoesNotDuplicateEffect(t *testing.T) {
	downstream := &fakeDownstream{seen: map[string]bool{}}
	req := WriteRequest{WorkflowID: "charge-order-42", ActivityID: "charge-card", Payload: []byte("42")}

	// First attempt reaches the downstream system. The Worker then crashes before
	// Temporal records completion, so the same Activity is retried.
	if err := ExecuteIdempotentWrite(context.Background(), downstream, req); err != nil {
		t.Fatal(err)
	}
	if err := ExecuteIdempotentWrite(context.Background(), downstream, req); err != nil {
		t.Fatal(err)
	}
	if downstream.effects != 1 {
		t.Fatalf("external effects = %d, want 1", downstream.effects)
	}
	if downstream.calls != 2 {
		t.Fatalf("downstream calls = %d, want retry to be observable", downstream.calls)
	}
}

type fakeDownstream struct {
	seen           map[string]bool
	calls, effects int
}

func (f *fakeDownstream) ApplyOnce(_ context.Context, key string, _ []byte) error {
	f.calls++
	if key == "" {
		return errors.New("missing idempotency key")
	}
	if !f.seen[key] {
		f.seen[key] = true
		f.effects++
	}
	return nil
}
