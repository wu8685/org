package hello

import (
	"testing"

	"github.com/wu8685/org/sdk/orgsdk"
)

func TestHelloDefinitionUsesOnlyOrgSDKAndDeclaresSequentialSemanticGraph(t *testing.T) {
	worker, err := NewWorker("v1")
	if err != nil {
		t.Fatal(err)
	}
	if worker.Definition.Name != "hello" || len(worker.Definition.Templates) != 3 {
		t.Fatalf("definition = %#v", worker.Definition)
	}
	want := []struct {
		id       string
		nodeType orgsdk.NodeType
	}{{"prepare-greeting", orgsdk.NodeTypeActivity}, {"compose-greeting", orgsdk.NodeTypeActivity}, {"completed", orgsdk.NodeTypeSemantic}}
	for i, expected := range want {
		template := worker.Definition.Templates[i]
		if template.ID != expected.id || template.Type != expected.nodeType {
			t.Fatalf("template %d = %#v", i, template)
		}
		if template.Type == orgsdk.NodeTypeActivity && (template.Activity == nil || template.Activity.SideEffect != orgsdk.SideEffectNone) {
			t.Fatalf("Activity policy = %#v", template.Activity)
		}
	}
}

func TestHelloWorkerRunsThroughOrgSDKTestkit(t *testing.T) {
	worker, err := NewWorker("v1")
	if err != nil {
		t.Fatal(err)
	}
	env := orgsdk.NewTestEnvironment()
	if err := env.Register(worker.Registrations()...); err != nil {
		t.Fatal(err)
	}
	env.ExecuteWorkflow(WorkflowName, GreetingInput{Name: " Codex "})
	if err := env.WorkflowError(); err != nil {
		t.Fatal(err)
	}
	var result GreetingResult
	if err := env.Result(&result); err != nil {
		t.Fatal(err)
	}
	if result.Message != "Hello, Codex!" || result.WorkerVersion != "v1" || len(result.IdempotencyKey) != 64 {
		t.Fatalf("result = %#v", result)
	}
	projection, err := env.Projection()
	if err != nil {
		t.Fatal(err)
	}
	if projection.Status != "completed" || len(projection.Nodes) != 3 {
		t.Fatalf("projection = %#v", projection)
	}
	for i, status := range []orgsdk.NodeStatus{orgsdk.NodeStatusCompleted, orgsdk.NodeStatusCompleted, orgsdk.NodeStatusCompleted} {
		if projection.Nodes[i].TemplateID != worker.Definition.Templates[i].ID || projection.Nodes[i].Status != status {
			t.Fatalf("node %d = %#v", i, projection.Nodes[i])
		}
	}
	if projection.Nodes[1].Dependencies[0] != projection.Nodes[0].RuntimeNodeID || projection.Nodes[2].Dependencies[0] != projection.Nodes[1].RuntimeNodeID {
		t.Fatalf("dependencies = %#v", projection.Nodes)
	}
}
