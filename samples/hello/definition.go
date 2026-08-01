package hello

import (
	"time"

	"github.com/wu8685/org/sdk/orgsdk"
)

type Worker struct {
	Definition orgsdk.Definition
	prepare    orgsdk.ActivityDefinition[GreetingInput, GreetingContext]
	compose    orgsdk.ActivityDefinition[GreetingContext, GreetingResult]
	workflow   orgsdk.WorkflowDefinition[GreetingInput, GreetingResult]
}

func NewWorker(version string) (Worker, error) {
	policy := orgsdk.ActivityPolicy{
		SideEffect: orgsdk.SideEffectNone,
		Retry: orgsdk.RetryPolicy{
			InitialInterval: 100 * time.Millisecond, BackoffCoefficient: 2,
			MaximumInterval: 2 * time.Second, MaximumAttempts: 3, StartToCloseTimeout: 5 * time.Second,
		},
	}
	prepare := orgsdk.NewActivity(prepareGreetingActivityID, policy, func(_ orgsdk.ActivityContext, input GreetingInput) (GreetingContext, error) {
		return PrepareGreeting(input)
	})
	compose := orgsdk.NewActivity(composeGreetingActivityID, policy, func(ctx orgsdk.ActivityContext, greeting GreetingContext) (GreetingResult, error) {
		key, err := StableIdempotencyKey(ctx.WorkflowID, ctx.ActivityID)
		if err != nil {
			return GreetingResult{}, err
		}
		return ComposeGreeting(greeting, version, key)
	})
	definition := orgsdk.NewDefinition[GreetingInput, GreetingResult]("hello", []orgsdk.NodeTemplate{
		orgsdk.ActivityNode(prepare, "Prepare greeting", orgsdk.CardinalitySingleton),
		orgsdk.ActivityNode(compose, "Compose greeting", orgsdk.CardinalitySingleton),
		{ID: "completed", Label: "Completed", Type: orgsdk.NodeTypeSemantic},
	}, orgsdk.RuntimeBounds{MaxInstancesPerFanOut: 1, MaxRuntimeNodes: 3, MaxProjectionBytes: 64 << 10})
	workflowDefinition, err := orgsdk.NewWorkflowDefinition(WorkflowName, version, definition, func(ctx *orgsdk.WorkflowContext, input GreetingInput) (GreetingResult, error) {
		prepareNode, greeting, err := orgsdk.ExecuteActivity(ctx, prepare, "singleton", orgsdk.NodeRef{}, nil, input, "")
		if err != nil {
			return GreetingResult{}, err
		}
		composeNode, result, err := orgsdk.ExecuteActivity(ctx, compose, "singleton", prepareNode, []orgsdk.NodeRef{prepareNode}, greeting, "")
		if err != nil {
			return GreetingResult{}, err
		}
		if _, err := orgsdk.CompleteSemantic(ctx, "completed", "singleton", composeNode, []orgsdk.NodeRef{composeNode}); err != nil {
			return GreetingResult{}, err
		}
		return result, nil
	})
	if err != nil {
		return Worker{}, err
	}
	return Worker{Definition: definition, prepare: prepare, compose: compose, workflow: workflowDefinition}, nil
}

func (worker Worker) Registrations() []orgsdk.Registration {
	return []orgsdk.Registration{worker.prepare, worker.compose, worker.workflow}
}

func (worker Worker) Manifest() ([]byte, string, error) {
	return orgsdk.GenerateManifest(WorkflowName, worker.Definition)
}
