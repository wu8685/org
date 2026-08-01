package parallelconfirmation

import (
	"encoding/json"
	"sync/atomic"
	"time"

	"github.com/wu8685/org/sdk/orgsdk"
)

type Worker struct {
	Definition orgsdk.Definition
	buildPlan  orgsdk.ActivityDefinition[Input, Plan]
	branch     orgsdk.ActivityDefinition[Branch, BranchResult]
	finalize   orgsdk.ActivityDefinition[FinalizeInput, Result]
	workflow   orgsdk.WorkflowDefinition[Input, Result]
	calls      *atomic.Int32
}

func NewWorker(version string) (Worker, error) {
	calls := &atomic.Int32{}
	policy := orgsdk.ActivityPolicy{
		SideEffect: orgsdk.SideEffectNone,
		Retry: orgsdk.RetryPolicy{
			InitialInterval: 100 * time.Millisecond, BackoffCoefficient: 2,
			MaximumInterval: 2 * time.Second, MaximumAttempts: 3, StartToCloseTimeout: 10 * time.Second,
		},
	}
	buildPlan := orgsdk.NewActivity("build-plan", policy, func(_ orgsdk.ActivityContext, input Input) (Plan, error) {
		calls.Add(1)
		return BuildPlan(input)
	})
	branch := orgsdk.NewActivity("execute-branch", policy, func(_ orgsdk.ActivityContext, input Branch) (BranchResult, error) {
		calls.Add(1)
		return ExecuteBranch(input)
	})
	finalize := orgsdk.NewActivity("finalize", policy, func(_ orgsdk.ActivityContext, input FinalizeInput) (Result, error) {
		calls.Add(1)
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
