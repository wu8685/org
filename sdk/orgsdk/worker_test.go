package orgsdk

import (
	"strings"
	"testing"
	"time"
)

func TestWorkerConfigRequiresPlatformRoutingAndManifestIdentity(t *testing.T) {
	valid := WorkerConfig{
		TemporalAddress: "127.0.0.1:7233", TemporalNamespace: "default",
		TaskQueue: "org-tenant-worker", DeploymentName: "org-tenant-worker", BuildID: "v1",
		ManifestDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid config: %v", err)
	}
	valid.TaskQueue = ""
	if err := valid.Validate(); err == nil || !strings.Contains(err.Error(), "Task Queue") {
		t.Fatalf("missing routing error = %v", err)
	}
}

func TestSDKRegistrationsHideRawTemporalRegistration(t *testing.T) {
	policy := ActivityPolicy{SideEffect: SideEffectNone, Retry: RetryPolicy{MaximumAttempts: 1, StartToCloseTimeout: time.Second}}
	activity := NewActivity("prepare", policy, func(_ ActivityContext, input string) (string, error) { return input, nil })
	contract := Definition{
		Name: "hello", Bounds: RuntimeBounds{MaxInstancesPerFanOut: 2, MaxRuntimeNodes: 3, MaxProjectionBytes: 4096},
		Templates: []NodeTemplate{{ID: "prepare", Label: "Prepare", Type: NodeTypeActivity, Activity: &policy}},
	}
	workflowDefinition, err := NewWorkflowDefinition("HelloWorkflow", "v1", contract, func(ctx *WorkflowContext, input string) (string, error) {
		_, output, err := ExecuteActivity(ctx, activity, "singleton", NodeRef{}, nil, input, "")
		return output, err
	})
	if err != nil {
		t.Fatal(err)
	}
	sink := &recordingRegistrationSink{}
	if err := registerAll(sink, []Registration{activity, workflowDefinition}); err != nil {
		t.Fatal(err)
	}
	if len(sink.activities) != 1 || sink.activities[0] != "prepare" || len(sink.workflows) != 1 || sink.workflows[0] != "HelloWorkflow" {
		t.Fatalf("registrations activities=%v workflows=%v", sink.activities, sink.workflows)
	}
}

type recordingRegistrationSink struct {
	activities []string
	workflows  []string
}

func (r *recordingRegistrationSink) registerActivity(name string, _ any) error {
	r.activities = append(r.activities, name)
	return nil
}

func (r *recordingRegistrationSink) registerWorkflow(name string, _ any) error {
	r.workflows = append(r.workflows, name)
	return nil
}
