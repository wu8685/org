package hello

import (
	"testing"

	"github.com/wu8685/org/sdk/orgsdk"
)

func TestHelloProjectionNodeIDsAndDependenciesAreReplayStable(t *testing.T) {
	first := executeHello(t, "v1", "Codex")
	second := executeHello(t, "v1", "Codex")
	if len(first.Nodes) != len(second.Nodes) {
		t.Fatalf("node counts differ: first=%d second=%d", len(first.Nodes), len(second.Nodes))
	}
	for i := range first.Nodes {
		left, right := first.Nodes[i], second.Nodes[i]
		if left.RuntimeNodeID != right.RuntimeNodeID || left.TemplateID != right.TemplateID || left.Status != right.Status || len(left.Dependencies) != len(right.Dependencies) {
			t.Fatalf("projection identity is not replay-stable:\nfirst=%#v\nsecond=%#v", first.Nodes, second.Nodes)
		}
		for j := range left.Dependencies {
			if left.Dependencies[j] != right.Dependencies[j] {
				t.Fatalf("projection dependencies are not replay-stable:\nfirst=%#v\nsecond=%#v", first.Nodes, second.Nodes)
			}
		}
	}
}

func executeHello(t *testing.T, version, name string) orgsdk.Projection {
	t.Helper()
	worker, err := NewWorker(version)
	if err != nil {
		t.Fatal(err)
	}
	env := orgsdk.NewTestEnvironment()
	if err := env.Register(worker.Registrations()...); err != nil {
		t.Fatal(err)
	}
	env.ExecuteWorkflow(WorkflowName, GreetingInput{Name: name})
	if err := env.WorkflowError(); err != nil {
		t.Fatal(err)
	}
	projection, err := env.Projection()
	if err != nil {
		t.Fatal(err)
	}
	return projection
}
