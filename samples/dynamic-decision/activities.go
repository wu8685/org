package dynamicdecision

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

const (
	minDemoDelay = 2 * time.Second
	maxDemoDelay = 5 * time.Second
)

func randomDemoDelay(_ string) (time.Duration, error) {
	seconds, err := rand.Int(rand.Reader, big.NewInt(int64(maxDemoDelay/time.Second-minDemoDelay/time.Second+1)))
	if err != nil {
		return 0, err
	}
	return minDemoDelay + time.Duration(seconds.Int64())*time.Second, nil
}

func sleepDemoActivity(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func DetermineRoute(input Input) (Route, error) {
	mode := strings.ToLower(strings.TrimSpace(input.Mode))
	subject := strings.TrimSpace(input.Subject)
	if subject == "" {
		return Route{}, errors.New("subject is required")
	}
	if mode != "concise" && mode != "detailed" {
		return Route{}, fmt.Errorf("unsupported route %q", mode)
	}
	return Route{Name: mode, Subject: subject}, nil
}

func RunConcise(input BranchInput) (BranchResult, error) {
	if strings.TrimSpace(input.Subject) == "" {
		return BranchResult{}, errors.New("subject is required")
	}
	return BranchResult{Route: "concise", Content: "Concise result for " + input.Subject}, nil
}

func RunDetailed(input BranchInput) (BranchResult, error) {
	if strings.TrimSpace(input.Subject) == "" {
		return BranchResult{}, errors.New("subject is required")
	}
	return BranchResult{Route: "detailed", Content: "Detailed result for " + input.Subject + " with additional context"}, nil
}

func Finalize(input FinalizeInput) (Result, error) {
	if input.Subject == "" || input.Selected.Route == "" || input.Selected.Content == "" || input.WorkerVersion == "" {
		return Result{}, errors.New("subject, selected branch, and Worker version are required")
	}
	return Result{Subject: input.Subject, Route: input.Selected.Route, Content: input.Selected.Content, WorkerVersion: input.WorkerVersion}, nil
}
