package temporal

import (
	"strings"
	"testing"

	"github.com/wu8685/org/internal/domain"
	"github.com/wu8685/org/internal/service"
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
	version := domain.WorkerVersion{ID: "ver-123", TaskQueue: "org-alpha-worker", WorkerDeployment: "org-alpha-worker", Version: "v2"}
	opts := buildProbeStartOptions(version)
	if opts.TaskQueue != version.TaskQueue || !strings.Contains(opts.ID, version.ID) {
		t.Fatalf("probe options = %#v", opts)
	}
	override, ok := opts.VersioningOverride.(*client.PinnedVersioningOverride)
	if !ok || override.Version.DeploymentName != version.WorkerDeployment || override.Version.BuildID != version.Version {
		t.Fatalf("probe version override = %#v", opts.VersioningOverride)
	}
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
