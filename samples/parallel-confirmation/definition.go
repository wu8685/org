package parallelconfirmation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/wu8685/org/sdk/orgsdk"
)

type demoDelaySource func(string) (time.Duration, error)
type demoActivitySleeper func(context.Context, time.Duration) error

type workerConfig struct {
	delay demoDelaySource
	sleep demoActivitySleeper
}

type WorkerOption func(*workerConfig)

func withDemoDelaySource(source demoDelaySource) WorkerOption {
	return func(config *workerConfig) { config.delay = source }
}

func withDemoActivitySleeper(sleeper demoActivitySleeper) WorkerOption {
	return func(config *workerConfig) { config.sleep = sleeper }
}

type Worker struct {
	Definition orgsdk.Definition
	buildPlan  orgsdk.ActivityDefinition[Input, Plan]
	branch     orgsdk.ActivityDefinition[Branch, BranchResult]
	finalize   orgsdk.ActivityDefinition[FinalizeInput, Result]
	workflow   orgsdk.WorkflowDefinition[Input, Result]
	calls      *atomic.Int32
}

func NewWorker(version string, options ...WorkerOption) (Worker, error) {
	config := workerConfig{delay: randomDemoDelay, sleep: sleepDemoActivity}
	for _, option := range options {
		option(&config)
	}
	if config.delay == nil || config.sleep == nil {
		return Worker{}, errors.New("parallel-confirmation demo delay source and sleeper are required")
	}
	wait := func(ctx context.Context, activity string) error {
		delay, err := config.delay(activity)
		if err != nil {
			return err
		}
		if delay < minDemoDelay || delay > maxDemoDelay {
			return fmt.Errorf("%s demo delay %s is outside [2s, 5s]", activity, delay)
		}
		return config.sleep(ctx, delay)
	}
	calls := &atomic.Int32{}
	policy := orgsdk.ActivityPolicy{
		SideEffect: orgsdk.SideEffectNone,
		Retry: orgsdk.RetryPolicy{
			InitialInterval: 100 * time.Millisecond, BackoffCoefficient: 2,
			MaximumInterval: 2 * time.Second, MaximumAttempts: 3, StartToCloseTimeout: 10 * time.Second,
		},
	}
	buildPlan := orgsdk.NewActivity("build-plan", policy, func(ctx orgsdk.ActivityContext, input Input) (Plan, error) {
		calls.Add(1)
		if err := wait(ctx.Context, "build-plan"); err != nil {
			return Plan{}, err
		}
		return BuildPlan(input)
	})
	branch := orgsdk.NewActivity("execute-branch", policy, func(ctx orgsdk.ActivityContext, input Branch) (BranchResult, error) {
		calls.Add(1)
		if err := wait(ctx.Context, "execute-branch/"+input.Key); err != nil {
			return BranchResult{}, err
		}
		return ExecuteBranch(input)
	})
	finalize := orgsdk.NewActivity("finalize", policy, func(ctx orgsdk.ActivityContext, input FinalizeInput) (Result, error) {
		calls.Add(1)
		if err := wait(ctx.Context, "finalize"); err != nil {
			return Result{}, err
		}
		return Finalize(input)
	})
	definition := orgsdk.NewDefinition[Input, Result]("parallel-confirmation", []orgsdk.NodeTemplate{
		{
			ID: "approval-gate", Label: "Confirm start", Type: orgsdk.NodeTypeWaitForAction, Cardinality: orgsdk.CardinalitySingleton,
			Actions: []orgsdk.ActionDefinition{{
				Name: "confirm", Label: "Confirm", RequiredPermission: "run:action:confirm",
				InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`),
			}},
		},
		orgsdk.ActivityNode(buildPlan, "Build plan", orgsdk.CardinalitySingleton),
		orgsdk.ActivityNode(branch, "Execute branch", orgsdk.CardinalityRepeated),
		{ID: "join", Label: "Join branches", Type: orgsdk.NodeTypeSemantic, Cardinality: orgsdk.CardinalitySingleton},
		orgsdk.ActivityNode(finalize, "Finalize", orgsdk.CardinalitySingleton),
	}, orgsdk.RuntimeBounds{MaxInstancesPerFanOut: 4, MaxRuntimeNodes: 8, MaxProjectionBytes: 128 << 10})
	workflowDefinition, err := orgsdk.NewWorkflowDefinition(WorkflowName, version, definition, func(ctx *orgsdk.WorkflowContext, input Input) (Result, error) {
		confirmation, err := orgsdk.AwaitConfirmation(ctx, "approval-gate", "singleton", nil, 24*time.Hour)
		if err != nil {
			return Result{}, err
		}
		planNode, plan, err := orgsdk.ExecuteActivity(ctx, buildPlan, "singleton", confirmation.Node, []orgsdk.NodeRef{confirmation.Node}, input, "")
		if err != nil {
			return Result{}, err
		}
		futures := make([]orgsdk.ActivityFuture[BranchResult], 0, len(plan.Branches))
		for _, item := range plan.Branches {
			future, err := orgsdk.StartActivity(ctx, branch, item.Key, planNode, []orgsdk.NodeRef{planNode}, item, "")
			if err != nil {
				return Result{}, err
			}
			futures = append(futures, future)
		}
		branchNodes := make([]orgsdk.NodeRef, 0, len(futures))
		branchResults := make([]BranchResult, 0, len(futures))
		for _, future := range futures {
			node, result, err := future.Get()
			if err != nil {
				return Result{}, err
			}
			branchNodes = append(branchNodes, node)
			branchResults = append(branchResults, result)
		}
		joinNode, err := orgsdk.CompleteSemantic(ctx, "join", "singleton", planNode, branchNodes)
		if err != nil {
			return Result{}, err
		}
		_, result, err := orgsdk.ExecuteActivity(ctx, finalize, "singleton", joinNode, []orgsdk.NodeRef{joinNode}, FinalizeInput{Subject: plan.Subject, Branches: branchResults, WorkerVersion: version}, "")
		return result, err
	})
	if err != nil {
		return Worker{}, err
	}
	return Worker{Definition: definition, buildPlan: buildPlan, branch: branch, finalize: finalize, workflow: workflowDefinition, calls: calls}, nil
}

func (worker Worker) Registrations() []orgsdk.Registration {
	return []orgsdk.Registration{worker.buildPlan, worker.branch, worker.finalize, worker.workflow}
}

func (worker Worker) Manifest() ([]byte, string, error) {
	return orgsdk.GenerateManifest(WorkflowName, worker.Definition)
}

func (worker Worker) ActivityCalls() int32 { return worker.calls.Load() }
