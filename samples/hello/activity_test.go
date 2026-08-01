package hello

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPrepareGreetingTrimsNameIntoContext(t *testing.T) {
	context, err := PrepareGreeting(GreetingInput{Name: "  Ada  "})
	if err != nil {
		t.Fatal(err)
	}
	if context.Name != "Ada" {
		t.Fatalf("context = %#v", context)
	}
}

func TestActivitySleepReturnsPromptlyWhenCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	err := sleepActivity(ctx, 10*time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("sleep error = %v", err)
	}
	if time.Since(started) > 100*time.Millisecond {
		t.Fatalf("canceled Activity sleep blocked for %s", time.Since(started))
	}
}

func TestPrepareGreetingRejectsEmptyName(t *testing.T) {
	if _, err := PrepareGreeting(GreetingInput{Name: "   "}); err == nil {
		t.Fatal("expected empty name error")
	}
}

func TestComposeGreetingUsesExplicitVersionAndStableKey(t *testing.T) {
	key := strings.Repeat("a", 64)
	result, err := ComposeGreeting(GreetingContext{Name: "Ada"}, "2026.08.1", key)
	if err != nil {
		t.Fatal(err)
	}
	if result.Message != "Hello, Ada!" || result.WorkerVersion != "2026.08.1" || result.IdempotencyKey != key {
		t.Fatalf("result = %#v", result)
	}
}

func TestStableIdempotencyKeyIsRepeatable(t *testing.T) {
	first, err := StableIdempotencyKey("workflow-42", "prepare-greeting")
	if err != nil {
		t.Fatal(err)
	}
	second, err := StableIdempotencyKey("workflow-42", "prepare-greeting")
	if err != nil {
		t.Fatal(err)
	}
	if first == "" || first != second {
		t.Fatalf("keys = %q, %q", first, second)
	}
}
