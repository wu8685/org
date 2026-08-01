package temporal

import (
	"context"
	"strings"
	"testing"

	"github.com/wu8685/org/internal/domain"
	"github.com/wu8685/org/internal/service"
	"github.com/wu8685/org/sdk/orgsdk"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
)

func TestStartOptionsUseIndependentWorkflowIDAndNoOverrideForCurrent(t *testing.T) {
	opts := buildStartOptions(service.ExecutionStart{InvocationID: "inv-1", WorkflowID: "org-alpha-payments-hash-run-opaque", TaskQueue: "org-alpha-payments-hash", DeploymentName: "org-alpha-payments-hash"})
	if opts.ID != "org-alpha-payments-hash-run-opaque" || opts.TaskQueue != "org-alpha-payments-hash" {
		t.Fatalf("options = %#v", opts)
	}
	if opts.VersioningOverride != nil {
		t.Fatalf("current version should use server Current, got %#v", opts.VersioningOverride)
	}
}

func TestContractProbeIsPinnedToTheCandidateWorkerVersion(t *testing.T) {
	version := domain.WorkerVersion{ID: "ver-123", PromotionAttemptID: "promotion-opaque-1", TaskQueue: "org-alpha-worker", WorkerDeployment: "org-alpha-worker", Version: "v2"}
	opts := buildProbeStartOptions(version)
	if opts.TaskQueue != version.TaskQueue || !strings.Contains(opts.ID, version.ID) || !strings.Contains(opts.ID, version.PromotionAttemptID) {
		t.Fatalf("probe options = %#v", opts)
	}
	override, ok := opts.VersioningOverride.(*client.PinnedVersioningOverride)
	if !ok || override.Version.DeploymentName != version.WorkerDeployment || override.Version.BuildID != version.Version {
		t.Fatalf("probe version override = %#v", opts.VersioningOverride)
	}
}

func TestContractProbeAttachesToTheSameAttemptAfterStartResponseIsLost(t *testing.T) {
	version := domain.WorkerVersion{ID: "ver-123", PromotionAttemptID: "promotion-opaque-1", TaskQueue: "org-alpha-worker", WorkerDeployment: "org-alpha-worker", Version: "v2"}
	want := orgsdk.ContractProbe{ManifestDigest: "sha256:contract", SDKModuleVersion: "sdk-v1", RuntimeProtocolVersion: "runtime-v1", WorkerBuildID: "v2"}
	sdk := &probeSDKStub{
		executeErr: &serviceerror.WorkflowExecutionAlreadyStarted{RunId: "run-existing"},
		run:        probeRunStub{result: want},
	}
	got, err := runContractProbe(context.Background(), sdk, version)
	if err != nil {
		t.Fatal(err)
	}
	if got != want || sdk.attachedWorkflowID != buildProbeStartOptions(version).ID || sdk.attachedRunID != "run-existing" {
		t.Fatalf("probe=%#v attached=%q/%q", got, sdk.attachedWorkflowID, sdk.attachedRunID)
	}
}

func TestWorkflowStartReturnsExistingRunAfterAmbiguousStartResponse(t *testing.T) {
	start := service.ExecutionStart{InvocationID: "inv-1", WorkflowID: "workflow-1", Workflow: "ChargeOrder", Input: []byte(`{}`), TaskQueue: "queue-1", DeploymentName: "deployment-1", PinnedVersion: "v1"}
	sdk := &probeSDKStub{
		executeErr: &serviceerror.WorkflowExecutionAlreadyStarted{RunId: "run-existing"},
		run:        probeRunStub{runID: "run-existing"},
	}
	runID, err := runWorkflowStart(context.Background(), sdk, start)
	if err != nil || runID != "run-existing" || sdk.attachedWorkflowID != start.WorkflowID || sdk.attachedRunID != "run-existing" {
		t.Fatalf("runID=%q error=%v attached=%q/%q", runID, err, sdk.attachedWorkflowID, sdk.attachedRunID)
	}
}

type probeSDKStub struct {
	executeErr                        error
	run                               client.WorkflowRun
	attachedWorkflowID, attachedRunID string
}

func (s *probeSDKStub) ExecuteWorkflow(context.Context, client.StartWorkflowOptions, interface{}, ...interface{}) (client.WorkflowRun, error) {
	return nil, s.executeErr
}

func (s *probeSDKStub) GetWorkflow(_ context.Context, workflowID, runID string) client.WorkflowRun {
	s.attachedWorkflowID, s.attachedRunID = workflowID, runID
	return s.run
}

type probeRunStub struct {
	result orgsdk.ContractProbe
	runID  string
}

func (s probeRunStub) Get(_ context.Context, value interface{}) error {
	*(value.(*orgsdk.ContractProbe)) = s.result
	return nil
}
func (probeRunStub) GetID() string      { return "" }
func (s probeRunStub) GetRunID() string { return s.runID }
func (probeRunStub) GetWithOptions(context.Context, interface{}, client.WorkflowRunGetOptions) error {
	return nil
}

func TestPollerReadinessRequiresWorkflowTaskQueue(t *testing.T) {
	activityOnly := []client.WorkerDeploymentTaskQueueInfo{{Name: "org-worker", Type: client.TaskQueueTypeActivity}}
	if hasWorkflowPoller(activityOnly, "org-worker") {
		t.Fatal("Activity poller was mistaken for Workflow readiness")
	}
	withWorkflow := append(activityOnly, client.WorkerDeploymentTaskQueueInfo{Name: "org-worker", Type: client.TaskQueueTypeWorkflow})
	if !hasWorkflowPoller(withWorkflow, "org-worker") {
		t.Fatal("Workflow poller was not recognized")
	}
}

func TestStartOptionsUsePinnedVersioningOverrideForExplicitHistory(t *testing.T) {
	opts := buildStartOptions(service.ExecutionStart{InvocationID: "inv-2", WorkflowID: "org-alpha-payments-hash-run-opaque", TaskQueue: "org-alpha-payments-hash", DeploymentName: "org-alpha-payments-hash", PinnedVersion: "v1"})
	override, ok := opts.VersioningOverride.(*client.PinnedVersioningOverride)
	if !ok {
		t.Fatalf("override = %#v", opts.VersioningOverride)
	}
	if override.Version.DeploymentName != "org-alpha-payments-hash" || override.Version.BuildID != "v1" {
		t.Fatalf("version = %#v", override.Version)
	}
}
