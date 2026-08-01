package orgsdk

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestTypedActivityFailureProducesBoundedSemanticFailure(t *testing.T) {
	projection := executeFailureWorkflow(t, func() error {
		return NewUserError("invalid_route", "Unsupported mode. Choose concise or detailed.")
	})
	if projection.Failure == nil {
		t.Fatalf("projection failure missing: %#v", projection)
	}
	failure := projection.Failure
	if failure.Code != "invalid_route" || failure.Message != "Unsupported mode. Choose concise or detailed." || failure.TemplateID != "determine-route" || failure.NodeLabel != "Determine route" || failure.RuntimeNodeID == "" || failure.OccurredAt.IsZero() {
		t.Fatalf("failure=%#v", failure)
	}
	if projection.Nodes[0].Status != NodeStatusFailed || projection.Nodes[0].ReasonCode != "invalid_route" {
		t.Fatalf("failed node=%#v", projection.Nodes[0])
	}
}

func TestRawAndMalformedActivityFailuresNeverEnterProjection(t *testing.T) {
	for name, failure := range map[string]func() error{
		"raw":       func() error { return errors.New("panic stack secret-token=abc") },
		"bad-code":  func() error { return NewUserError("INVALID/CODE", "must not be shown") },
		"oversized": func() error { return NewUserError("too_large", strings.Repeat("x", 301)) },
	} {
		t.Run(name, func(t *testing.T) {
			projection := executeFailureWorkflow(t, failure)
			if projection.Failure == nil || projection.Failure.Code != "activity_failed" || projection.Failure.Message != "Activity failed. Open advanced diagnostics if authorized." {
				t.Fatalf("failure=%#v", projection.Failure)
			}
			encoded := projection.Failure.Code + projection.Failure.Message
			for _, forbidden := range []string{"secret-token", "must not be shown", strings.Repeat("x", 40)} {
				if strings.Contains(encoded, forbidden) {
					t.Fatalf("failure leaked %q: %#v", forbidden, projection.Failure)
				}
			}
		})
	}
}

func TestSuccessfulWorkflowHasNoFailure(t *testing.T) {
	policy := ActivityPolicy{SideEffect: SideEffectNone, Retry: RetryPolicy{MaximumAttempts: 1, StartToCloseTimeout: time.Second}}
	activity := NewActivity("success", policy, func(_ ActivityContext, input string) (string, error) { return input, nil })
	definition := Definition{Name: "success", Bounds: RuntimeBounds{MaxInstancesPerFanOut: 1, MaxRuntimeNodes: 2, MaxProjectionBytes: 8192}, Templates: []NodeTemplate{ActivityNode(activity, "Success", CardinalitySingleton)}}
	workflowDefinition, err := NewWorkflowDefinition("SuccessWorkflow", "v1", definition, func(ctx *WorkflowContext, input string) (string, error) {
		_, output, err := ExecuteActivity(ctx, activity, "singleton", NodeRef{}, nil, input, "")
		return output, err
	})
	if err != nil {
		t.Fatal(err)
	}
	env := NewTestEnvironment()
	if err := env.Register(activity, workflowDefinition); err != nil {
		t.Fatal(err)
	}
	env.ExecuteWorkflow("SuccessWorkflow", "ok")
	projection, err := env.Projection()
	if err != nil || projection.Failure != nil {
		t.Fatalf("projection=%#v err=%v", projection, err)
	}
}

func executeFailureWorkflow(t *testing.T, fail func() error) Projection {
	t.Helper()
	policy := ActivityPolicy{SideEffect: SideEffectNone, Retry: RetryPolicy{MaximumAttempts: 1, StartToCloseTimeout: time.Second}}
	activity := NewActivity("determine-route", policy, func(_ ActivityContext, _ string) (string, error) { return "", fail() })
	definition := Definition{Name: "failure", Bounds: RuntimeBounds{MaxInstancesPerFanOut: 1, MaxRuntimeNodes: 2, MaxProjectionBytes: 8192}, Templates: []NodeTemplate{ActivityNode(activity, "Determine route", CardinalitySingleton)}}
	workflowDefinition, err := NewWorkflowDefinition("FailureWorkflow", "v1", definition, func(ctx *WorkflowContext, input string) (string, error) {
		_, output, err := ExecuteActivity(ctx, activity, "singleton", NodeRef{}, nil, input, "")
		return output, err
	})
	if err != nil {
		t.Fatal(err)
	}
	env := NewTestEnvironment()
	if err := env.Register(activity, workflowDefinition); err != nil {
		t.Fatal(err)
	}
	env.ExecuteWorkflow("FailureWorkflow", "secret-input")
	if env.WorkflowError() == nil {
		t.Fatal("Workflow unexpectedly succeeded")
	}
	projection, err := env.Projection()
	if err != nil {
		t.Fatal(err)
	}
	return projection
}
