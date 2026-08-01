package dynamicdecision

import (
	"sync/atomic"
	"time"

	"github.com/wu8685/org/sdk/orgsdk"
)

type Worker struct {
	Definition orgsdk.Definition
	determine  orgsdk.ActivityDefinition[Input, Route]
	concise    orgsdk.ActivityDefinition[BranchInput, BranchResult]
	detailed   orgsdk.ActivityDefinition[BranchInput, BranchResult]
	finalize   orgsdk.ActivityDefinition[FinalizeInput, Result]
	workflow   orgsdk.WorkflowDefinition[Input, Result]
	calls      map[string]*atomic.Int32
}

func NewWorker(version string) (Worker, error) {
	calls := map[string]*atomic.Int32{
		"determine-route": {}, "concise-branch": {}, "detailed-branch": {}, "finalize": {},
	}
	policy := orgsdk.ActivityPolicy{
		SideEffect: orgsdk.SideEffectNone,
		Retry: orgsdk.RetryPolicy{
			InitialInterval: 100 * time.Millisecond, BackoffCoefficient: 2,
			MaximumInterval: 2 * time.Second, MaximumAttempts: 3, StartToCloseTimeout: 10 * time.Second,
		},
	}
	determine := orgsdk.NewActivity("determine-route", policy, func(_ orgsdk.ActivityContext, input Input) (Route, error) {
		calls["determine-route"].Add(1)
		return DetermineRoute(input)
	})
	concise := orgsdk.NewActivity("concise-branch", policy, func(_ orgsdk.ActivityContext, input BranchInput) (BranchResult, error) {
		calls["concise-branch"].Add(1)
		return RunConcise(input)
	})
	detailed := orgsdk.NewActivity("detailed-branch", policy, func(_ orgsdk.ActivityContext, input BranchInput) (BranchResult, error) {
		calls["detailed-branch"].Add(1)
		return RunDetailed(input)
	})
	finalize := orgsdk.NewActivity("finalize", policy, func(_ orgsdk.ActivityContext, input FinalizeInput) (Result, error) {
		calls["finalize"].Add(1)
		return Finalize(input)
	})
	definition := orgsdk.NewDefinition[Input, Result]("dynamic-decision", []orgsdk.NodeTemplate{
		orgsdk.ActivityNode(determine, "Determine route", orgsdk.CardinalitySingleton),
		orgsdk.ActivityNode(concise, "Concise branch", orgsdk.CardinalitySingleton),
		orgsdk.ActivityNode(detailed, "Detailed branch", orgsdk.CardinalitySingleton),
		orgsdk.ActivityNode(finalize, "Finalize", orgsdk.CardinalitySingleton),
	}, orgsdk.RuntimeBounds{MaxInstancesPerFanOut: 2, MaxRuntimeNodes: 4, MaxProjectionBytes: 128 << 10})
	workflowDefinition, err := orgsdk.NewWorkflowDefinition(WorkflowName, version, definition, func(ctx *orgsdk.WorkflowContext, input Input) (Result, error) {
		routeNode, route, err := orgsdk.ExecuteActivity(ctx, determine, "singleton", orgsdk.NodeRef{}, nil, input, "")
		if err != nil {
			return Result{}, err
		}
		var selectedNode, skippedNode orgsdk.NodeRef
		var selected BranchResult
		switch route.Name {
		case "concise":
			skippedNode, err = orgsdk.SkipNode(ctx, detailed.Name, "singleton", routeNode, []orgsdk.NodeRef{routeNode}, "route-not-selected")
			if err == nil {
				selectedNode, selected, err = orgsdk.ExecuteActivity(ctx, concise, "singleton", routeNode, []orgsdk.NodeRef{routeNode}, BranchInput{Subject: route.Subject}, "")
			}
		case "detailed":
			skippedNode, err = orgsdk.SkipNode(ctx, concise.Name, "singleton", routeNode, []orgsdk.NodeRef{routeNode}, "route-not-selected")
			if err == nil {
				selectedNode, selected, err = orgsdk.ExecuteActivity(ctx, detailed, "singleton", routeNode, []orgsdk.NodeRef{routeNode}, BranchInput{Subject: route.Subject}, "")
			}
		default:
			return Result{}, orgsdk.ErrInvalidRoute
		}
		if err != nil {
			return Result{}, err
		}
		_, result, err := orgsdk.ExecuteActivity(ctx, finalize, "singleton", selectedNode, []orgsdk.NodeRef{selectedNode, skippedNode}, FinalizeInput{Subject: route.Subject, Selected: selected, WorkerVersion: version}, "")
		return result, err
	})
	if err != nil {
		return Worker{}, err
	}
	return Worker{Definition: definition, determine: determine, concise: concise, detailed: detailed, finalize: finalize, workflow: workflowDefinition, calls: calls}, nil
}

func (worker Worker) Registrations() []orgsdk.Registration {
	return []orgsdk.Registration{worker.determine, worker.concise, worker.detailed, worker.finalize, worker.workflow}
}

func (worker Worker) Manifest() ([]byte, string, error) {
	return orgsdk.GenerateManifest(WorkflowName, worker.Definition)
}

func (worker Worker) Calls(name string) int32 {
	if counter := worker.calls[name]; counter != nil {
		return counter.Load()
	}
	return 0
}
