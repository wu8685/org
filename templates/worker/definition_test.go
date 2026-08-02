package worker

import (
	"testing"

	"github.com/wu8685/org/sdk/orgsdk"
)

func TestWorkflowRunsThroughOrgSDKTestkit(t *testing.T) {
	worker, err := NewWorker("v1")
	if err != nil {
		t.Fatal(err)
	}
	env := orgsdk.NewTestEnvironment()
	if err := env.Register(worker.Registrations()...); err != nil {
		t.Fatal(err)
	}
	env.ExecuteWorkflow(WorkflowName, Input{Value: "first run"})
	if err := env.WorkflowError(); err != nil {
		t.Fatal(err)
	}
	var result Result
	if err := env.Result(&result); err != nil {
		t.Fatal(err)
	}
	if result.Value != "first run" {
		t.Fatalf("result = %#v", result)
	}
	projection, err := env.Projection()
	if err != nil {
		t.Fatal(err)
	}
	if projection.Status != "completed" || len(projection.Nodes) != 2 {
		t.Fatalf("projection = %#v", projection)
	}
}
