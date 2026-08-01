package hello

import (
	"context"
	"errors"
	"time"

	"github.com/wu8685/org/sdk/orgsdk"
)

const defaultComposeGreetingDelay = 10 * time.Second

type activitySleeper func(context.Context, time.Duration) error

type workerConfig struct {
	composeGreetingDelay time.Duration
	sleep                activitySleeper
}

type WorkerOption func(*workerConfig)

// WithComposeGreetingDelay changes the teaching delay inside ComposeGreeting.
// Production Workers should remove artificial delays and expose real Activity work.
func WithComposeGreetingDelay(delay time.Duration) WorkerOption {
	return func(config *workerConfig) { config.composeGreetingDelay = delay }
}

func withActivitySleeper(sleep activitySleeper) WorkerOption {
	return func(config *workerConfig) { config.sleep = sleep }
}

type Worker struct {
	Definition orgsdk.Definition
	prepare    orgsdk.ActivityDefinition[GreetingInput, GreetingContext]
	compose    orgsdk.ActivityDefinition[GreetingContext, GreetingResult]
	workflow   orgsdk.WorkflowDefinition[GreetingInput, GreetingResult]
}

func NewWorker(version string, options ...WorkerOption) (Worker, error) {
	config := workerConfig{composeGreetingDelay: defaultComposeGreetingDelay, sleep: sleepActivity}
	for _, option := range options {
		option(&config)
	}
	if config.composeGreetingDelay < 0 {
		return Worker{}, errors.New("ComposeGreeting demo delay must not be negative")
	}
	if config.sleep == nil {
		return Worker{}, errors.New("ComposeGreeting Activity sleeper is required")
	}
	startToCloseTimeout := config.composeGreetingDelay + 20*time.Second
	if startToCloseTimeout < 30*time.Second {
		startToCloseTimeout = 30 * time.Second
	}
	preparePolicy := orgsdk.ActivityPolicy{
		SideEffect: orgsdk.SideEffectNone,
		Retry: orgsdk.RetryPolicy{
			InitialInterval: 100 * time.Millisecond, BackoffCoefficient: 2,
			MaximumInterval: 2 * time.Second, MaximumAttempts: 3, StartToCloseTimeout: 5 * time.Second,
		},
	}
	composePolicy := preparePolicy
	composePolicy.Retry.StartToCloseTimeout = startToCloseTimeout
	prepare := orgsdk.NewActivity(prepareGreetingActivityID, preparePolicy, func(_ orgsdk.ActivityContext, input GreetingInput) (GreetingContext, error) {
		return PrepareGreeting(input)
	})
	compose := orgsdk.NewActivity(composeGreetingActivityID, composePolicy, func(ctx orgsdk.ActivityContext, greeting GreetingContext) (GreetingResult, error) {
		if err := config.sleep(ctx.Context, config.composeGreetingDelay); err != nil {
			return GreetingResult{}, err
		}
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
