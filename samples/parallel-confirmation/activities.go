package parallelconfirmation

import (
	"errors"
	"fmt"
	"strings"
)

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
