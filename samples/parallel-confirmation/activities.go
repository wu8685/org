package parallelconfirmation

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

func BuildPlan(input Input) (Plan, error) {
	subject := strings.TrimSpace(input.Subject)
	if subject == "" {
		return Plan{}, errors.New("subject is required")
	}
	return Plan{Subject: subject, Branches: []Branch{
		{Key: "summary", Task: "prepare a concise summary"},
		{Key: "readiness", Task: "verify readiness"},
	}}, nil
}

func ExecuteBranch(branch Branch) (BranchResult, error) {
	if branch.Key == "" || branch.Task == "" {
		return BranchResult{}, errors.New("branch key and task are required")
	}
	return BranchResult{Key: branch.Key, Summary: fmt.Sprintf("%s completed", branch.Task)}, nil
}

func Finalize(input FinalizeInput) (Result, error) {
	if input.Subject == "" || input.WorkerVersion == "" || len(input.Branches) == 0 {
		return Result{}, errors.New("subject, Worker version, and branch results are required")
	}
	return Result{Subject: input.Subject, Branches: append([]BranchResult(nil), input.Branches...), WorkerVersion: input.WorkerVersion}, nil
}
