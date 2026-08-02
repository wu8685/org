package worker

import (
	"time"

	"github.com/wu8685/org/sdk/orgsdk"
)

type Worker struct {
	Definition orgsdk.Definition
	process    orgsdk.ActivityDefinition[Input, Result]
	workflow   orgsdk.WorkflowDefinition[Input, Result]
}

func NewWorker(version string) (Worker, error) {
	policy := orgsdk.ActivityPolicy{
		SideEffect: orgsdk.SideEffectNone,
		Retry: orgsdk.RetryPolicy{
			InitialInterval:     100 * time.Millisecond,
			BackoffCoefficient:  2,
			MaximumInterval:     2 * time.Second,
			MaximumAttempts:     3,
			StartToCloseTimeout: 30 * time.Second,
		},
	}
	process := orgsdk.NewActivity(processActivityID, policy, func(_ orgsdk.ActivityContext, input Input) (Result, error) {
		return Process(input)
	})
	definition := orgsdk.NewDefinition[Input, Result]("main", []orgsdk.NodeTemplate{
		orgsdk.ActivityNode(process, "Process", orgsdk.CardinalitySingleton),
		{ID: "completed", Label: "Completed", Type: orgsdk.NodeTypeSemantic},
	}, orgsdk.RuntimeBounds{
		MaxInstancesPerFanOut: 1,
		MaxRuntimeNodes:       2,
		MaxProjectionBytes:    64 << 10,
	})
	workflow, err := orgsdk.NewWorkflowDefinition(WorkflowName, version, definition,
		func(ctx *orgsdk.WorkflowContext, input Input) (Result, error) {
			processNode, result, err := orgsdk.ExecuteActivity(
				ctx, process, "singleton", orgsdk.NodeRef{}, nil, input, "",
			)
			if err != nil {
				return Result{}, err
			}
			if _, err := orgsdk.CompleteSemantic(
				ctx, "completed", "singleton", processNode, []orgsdk.NodeRef{processNode},
			); err != nil {
				return Result{}, err
			}
			return result, nil
		})
	if err != nil {
		return Worker{}, err
	}
	return Worker{Definition: definition, process: process, workflow: workflow}, nil
}

func (worker Worker) Registrations() []orgsdk.Registration {
	return []orgsdk.Registration{worker.process, worker.workflow}
}
