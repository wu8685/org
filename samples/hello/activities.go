package hello

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

func sleepActivity(ctx context.Context, delay time.Duration) error {
	if delay == 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func PrepareGreeting(input GreetingInput) (GreetingContext, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return GreetingContext{}, errors.New("name is required")
	}
	return GreetingContext{Name: name}, nil
}

func ComposeGreeting(greeting GreetingContext, workerVersion, idempotencyKey string) (GreetingResult, error) {
	if greeting.Name == "" {
		return GreetingResult{}, errors.New("greeting context requires a name")
	}
	if workerVersion == "" || idempotencyKey == "" {
		return GreetingResult{}, errors.New("Worker version and stable idempotency key are required")
	}
	return GreetingResult{
		Message: fmt.Sprintf("Hello, %s!", greeting.Name), WorkerVersion: workerVersion, IdempotencyKey: idempotencyKey,
	}, nil
}

func StableIdempotencyKey(workflowID, activityID string) (string, error) {
	if workflowID == "" || activityID == "" {
		return "", errors.New("workflow and Activity identity are required")
	}
	sum := sha256.Sum256([]byte(workflowID + "\x00" + activityID))
	return hex.EncodeToString(sum[:]), nil
}
