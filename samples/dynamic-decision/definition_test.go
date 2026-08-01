package dynamicdecision

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/wu8685/org/sdk/orgsdk"
)

func TestDefinitionDeclaresRecordedRouteBothCandidatesAndFinalize(t *testing.T) {
	worker := newTestWorker(t)
	want := []string{"determine-route", "concise-branch", "detailed-branch", "finalize"}
	if len(worker.Definition.Templates) != len(want) {
		t.Fatalf("templates = %#v", worker.Definition.Templates)
	}
	for i, id := range want {
		template := worker.Definition.Templates[i]
		if template.ID != id || template.Type != orgsdk.NodeTypeActivity || template.Cardinality != orgsdk.CardinalitySingleton {
			t.Fatalf("template %d = %#v", i, template)
		}
	}
}

func TestWorkflowExecutesOnlySelectedCandidateAndProjectsSkippedNode(t *testing.T) {
	tests := []struct {
		mode, selected, skipped string
	}{
		{mode: "concise", selected: "concise-branch", skipped: "detailed-branch"},
		{mode: "detailed", selected: "detailed-branch", skipped: "concise-branch"},
	}
	for _, test := range tests {
		t.Run(test.mode, func(t *testing.T) {
			worker := newTestWorker(t)
			env := orgsdk.NewTestEnvironment()
			if err := env.Register(worker.Registrations()...); err != nil {
				t.Fatal(err)
			}
			env.ExecuteWorkflow(WorkflowName, Input{Mode: test.mode, Subject: "release notes"})
			if err := env.WorkflowError(); err != nil {
				t.Fatal(err)
			}
			var result Result
			if err := env.Result(&result); err != nil {
				t.Fatal(err)
			}
			if result.Route != test.mode || result.WorkerVersion != "v1" {
				t.Fatalf("result = %#v", result)
			}
			if worker.Calls("determine-route") != 1 || worker.Calls(test.selected) != 1 || worker.Calls(test.skipped) != 0 || worker.Calls("finalize") != 1 {
				t.Fatalf("Activity calls: determine=%d selected=%d skipped=%d finalize=%d", worker.Calls("determine-route"), worker.Calls(test.selected), worker.Calls(test.skipped), worker.Calls("finalize"))
			}
			projection, err := env.Projection()
			if err != nil {
				t.Fatal(err)
			}
			if projection.Status != "completed" || len(projection.Nodes) != 4 {
				t.Fatalf("projection = %#v", projection)
			}
			selected := nodeByTemplate(projection, test.selected)
			skipped := nodeByTemplate(projection, test.skipped)
			finalize := nodeByTemplate(projection, "finalize")
			if selected.Status != orgsdk.NodeStatusCompleted || skipped.Status != orgsdk.NodeStatusSkipped || skipped.ReasonCode != "route-not-selected" || finalize.Status != orgsdk.NodeStatusCompleted || len(finalize.Dependencies) != 2 {
				t.Fatalf("selected=%#v skipped=%#v finalize=%#v", selected, skipped, finalize)
			}
		})
	}
}

func TestRuntimeNodeIDsAreStableForSameRoute(t *testing.T) {
	first := executeProjection(t, "concise")
	second := executeProjection(t, "concise")
	for _, templateID := range []string{"determine-route", "concise-branch", "detailed-branch", "finalize"} {
		if nodeByTemplate(first, templateID).RuntimeNodeID != nodeByTemplate(second, templateID).RuntimeNodeID {
			t.Fatalf("%s IDs differ: %q != %q", templateID, nodeByTemplate(first, templateID).RuntimeNodeID, nodeByTemplate(second, templateID).RuntimeNodeID)
		}
	}
}

func TestInvalidRouteFailsWithoutFallback(t *testing.T) {
	worker := newTestWorker(t)
	env := orgsdk.NewTestEnvironment()
	if err := env.Register(worker.Registrations()...); err != nil {
		t.Fatal(err)
	}
	env.ExecuteWorkflow(WorkflowName, Input{Mode: "automatic", Subject: "release notes"})
	if err := env.WorkflowError(); err == nil || !strings.Contains(err.Error(), "Unsupported mode. Choose concise or detailed.") || strings.Contains(err.Error(), `"automatic"`) {
		t.Fatalf("workflow error = %v", err)
	}
	if worker.Calls("concise-branch") != 0 || worker.Calls("detailed-branch") != 0 || worker.Calls("finalize") != 0 {
		t.Fatalf("fallback Activity was called: concise=%d detailed=%d finalize=%d", worker.Calls("concise-branch"), worker.Calls("detailed-branch"), worker.Calls("finalize"))
	}
	projection, err := env.Projection()
	if err != nil {
		t.Fatal(err)
	}
	if projection.Status != "failed" || nodeByTemplate(projection, "determine-route").Status != orgsdk.NodeStatusFailed {
		t.Fatalf("failure projection = %#v", projection)
	}
	if projection.Failure == nil || projection.Failure.Code != "invalid_route" || projection.Failure.Message != "Unsupported mode. Choose concise or detailed." || projection.Failure.TemplateID != "determine-route" || projection.Failure.NodeLabel != "Determine route" {
		t.Fatalf("safe failure projection = %#v", projection.Failure)
	}
}

func nodeByTemplate(projection orgsdk.Projection, templateID string) orgsdk.NodeProjection {
	for _, node := range projection.Nodes {
		if node.TemplateID == templateID {
			return node
		}
	}
	return orgsdk.NodeProjection{}
}

func executeProjection(t *testing.T, mode string) orgsdk.Projection {
	t.Helper()
	worker := newTestWorker(t)
	env := orgsdk.NewTestEnvironment()
	if err := env.Register(worker.Registrations()...); err != nil {
		t.Fatal(err)
	}
	env.ExecuteWorkflow(WorkflowName, Input{Mode: mode, Subject: "release notes"})
	if err := env.WorkflowError(); err != nil {
		t.Fatal(err)
	}
	projection, err := env.Projection()
	if err != nil {
		t.Fatal(err)
	}
	return projection
}

func newTestWorker(t *testing.T) Worker {
	t.Helper()
	worker, err := NewWorker("v1",
		withDemoDelaySource(func(string) (time.Duration, error) { return minDemoDelay, nil }),
		withDemoActivitySleeper(func(context.Context, time.Duration) error { return nil }),
	)
	if err != nil {
		t.Fatal(err)
	}
	return worker
}
