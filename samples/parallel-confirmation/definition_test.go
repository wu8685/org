package parallelconfirmation

import (
	"testing"
	"time"

	"github.com/wu8685/org/sdk/orgsdk"
)

func TestDefinitionDeclaresApprovalDynamicForkJoinAndFinalize(t *testing.T) {
	worker, err := NewWorker("v1")
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		id          string
		nodeType    orgsdk.NodeType
		cardinality orgsdk.Cardinality
	}{
		{"approval-gate", orgsdk.NodeTypeWaitForAction, orgsdk.CardinalitySingleton},
		{"build-plan", orgsdk.NodeTypeActivity, orgsdk.CardinalitySingleton},
		{"execute-branch", orgsdk.NodeTypeActivity, orgsdk.CardinalityRepeated},
		{"join", orgsdk.NodeTypeSemantic, orgsdk.CardinalitySingleton},
		{"finalize", orgsdk.NodeTypeActivity, orgsdk.CardinalitySingleton},
	}
	if len(worker.Definition.Templates) != len(want) {
		t.Fatalf("templates = %#v", worker.Definition.Templates)
	}
	for i, expected := range want {
		template := worker.Definition.Templates[i]
		if template.ID != expected.id || template.Type != expected.nodeType || template.Cardinality != expected.cardinality {
			t.Fatalf("template %d = %#v", i, template)
		}
	}
	actions := worker.Definition.Templates[0].Actions
	if len(actions) != 1 || actions[0].Name != "confirm" || actions[0].RequiredPermission != "run:action:confirm" {
		t.Fatalf("approval actions = %#v", actions)
	}
}

func TestWorkflowWaitsIdleThenRunsTwoBranchesAndJoins(t *testing.T) {
	worker, err := NewWorker("v1")
	if err != nil {
		t.Fatal(err)
	}
	env := orgsdk.NewTestEnvironment()
	if err := env.Register(worker.Registrations()...); err != nil {
		t.Fatal(err)
	}
	approvalID := orgsdk.RuntimeNodeID(worker.Definition.Name, "", "approval-gate", "singleton")
	env.After(time.Second, func() {
		projection, queryErr := env.Projection()
		if queryErr != nil {
			t.Errorf("waiting projection: %v", queryErr)
			return
		}
		if len(projection.Nodes) != 1 || projection.Node(approvalID).Status != orgsdk.NodeStatusWaitingForUser || len(projection.AllowedActions) != 1 || worker.ActivityCalls() != 0 {
			t.Errorf("idle projection=%#v Activity calls=%d", projection, worker.ActivityCalls())
		}
		env.SignalAction(orgsdk.ActionEnvelope{OperationID: "op-confirm", NodeID: approvalID, Action: "confirm", Input: []byte(`{}`)})
	})
	env.ExecuteWorkflow(WorkflowName, Input{Subject: "release notes"})
	if err := env.WorkflowError(); err != nil {
		t.Fatal(err)
	}
	var result Result
	if err := env.Result(&result); err != nil {
		t.Fatal(err)
	}
	if result.Subject != "release notes" || result.WorkerVersion != "v1" || len(result.Branches) != 2 || worker.ActivityCalls() != 4 {
		t.Fatalf("result=%#v Activity calls=%d", result, worker.ActivityCalls())
	}
	projection, err := env.Projection()
	if err != nil {
		t.Fatal(err)
	}
	if projection.Status != "completed" || len(projection.Nodes) != 6 || len(projection.ActionOutcomes) != 1 || projection.ActionOutcomes[0].State != orgsdk.ActionOutcomeAccepted {
		t.Fatalf("projection = %#v", projection)
	}
	statuses := map[string][]orgsdk.NodeProjection{}
	for _, node := range projection.Nodes {
		statuses[node.TemplateID] = append(statuses[node.TemplateID], node)
		if node.Status != orgsdk.NodeStatusCompleted {
			t.Fatalf("non-completed node = %#v", node)
		}
	}
	if len(statuses["execute-branch"]) != 2 || len(statuses["join"][0].Dependencies) != 2 || len(statuses["finalize"][0].Dependencies) != 1 {
		t.Fatalf("fork/join projection = %#v", projection.Nodes)
	}
}
