package orgsdk

import (
	"testing"
	"time"
)

func TestSDKTestEnvironmentRunsDefinitionWithoutRawTemporalAPI(t *testing.T) {
	policy := ActivityPolicy{SideEffect: SideEffectNone, Retry: RetryPolicy{MaximumAttempts: 1, StartToCloseTimeout: time.Second}}
	prepare := NewActivity("prepare", policy, func(_ ActivityContext, input string) (string, error) { return "hello " + input, nil })
	contract := Definition{
		Name: "testkit", Bounds: RuntimeBounds{MaxInstancesPerFanOut: 2, MaxRuntimeNodes: 2, MaxProjectionBytes: 8192},
		Templates: []NodeTemplate{{ID: "prepare", Label: "Prepare", Type: NodeTypeActivity, Activity: &policy}},
	}
	workflowDefinition, err := NewWorkflowDefinition("TestkitWorkflow", "v1", contract, func(ctx *WorkflowContext, input string) (string, error) {
		_, output, err := ExecuteActivity(ctx, prepare, "singleton", NodeRef{}, nil, input, "")
		return output, err
	})
	if err != nil {
		t.Fatal(err)
	}
	env := NewTestEnvironment()
	if err := env.Register(prepare, workflowDefinition); err != nil {
		t.Fatal(err)
	}
	env.ExecuteWorkflow("TestkitWorkflow", "Ada")
	if err := env.WorkflowError(); err != nil {
		t.Fatal(err)
	}
	var result string
	if err := env.Result(&result); err != nil || result != "hello Ada" {
		t.Fatalf("result=%q err=%v", result, err)
	}
	projection, err := env.Projection()
	if err != nil || len(projection.Nodes) != 1 || projection.Nodes[0].Status != NodeStatusCompleted || len(projection.RecentEvents) == 0 || projection.RecentEvents[len(projection.RecentEvents)-1].Type != EventGraphCompleted {
		t.Fatalf("projection=%#v err=%v", projection, err)
	}
}
