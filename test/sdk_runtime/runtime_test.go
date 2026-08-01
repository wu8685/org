package sdk_runtime_test

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wu8685/org/sdk/orgsdk"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

func TestOrgSDKWaitSurvivesWorkerRestartOnLocalTemporal(t *testing.T) {
	if os.Getenv("ORG_SDK_TEMPORAL_TEST") != "1" {
		t.Skip("set ORG_SDK_TEMPORAL_TEST=1 to use Temporal at 127.0.0.1:7233")
	}
	var activityCalls atomic.Int32
	policy := orgsdk.ActivityPolicy{SideEffect: orgsdk.SideEffectNone, Retry: orgsdk.RetryPolicy{MaximumAttempts: 1, StartToCloseTimeout: 10 * time.Second}}
	finish := orgsdk.NewActivity("finish", policy, func(_ orgsdk.ActivityContext, input string) (string, error) {
		activityCalls.Add(1)
		return input + "-confirmed", nil
	})
	definition := orgsdk.NewDefinition[string, string]("sdk-runtime-verification", []orgsdk.NodeTemplate{
		{
			ID: "approval", Label: "Approval", Type: orgsdk.NodeTypeWaitForAction,
			Actions: []orgsdk.ActionDefinition{{Name: "confirm", Label: "Confirm", RequiredPermission: "run:action:confirm"}},
		},
		orgsdk.ActivityNode(finish, "Finish", orgsdk.CardinalitySingleton),
	}, orgsdk.RuntimeBounds{MaxInstancesPerFanOut: 2, MaxRuntimeNodes: 4, MaxProjectionBytes: 64 << 10})
	workflowDefinition, err := orgsdk.NewWorkflowDefinition("SDKRuntimeVerificationWorkflow", "v1", definition, func(ctx *orgsdk.WorkflowContext, input string) (string, error) {
		confirmation, err := orgsdk.AwaitConfirmation(ctx, "approval", "singleton", nil, time.Hour)
		if err != nil {
			return "", err
		}
		_, output, err := orgsdk.ExecuteActivity(ctx, finish, "singleton", confirmation.Node, []orgsdk.NodeRef{confirmation.Node}, input, "")
		return output, err
	})
	if err != nil {
		t.Fatal(err)
	}
	_, manifestDigest, err := orgsdk.GenerateManifest(workflowDefinition.Name, definition)
	if err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	config := orgsdk.WorkerConfig{
		TemporalAddress: "127.0.0.1:7233", TemporalNamespace: "default",
		TaskQueue: "org-sdk-test-" + suffix, DeploymentName: "org-sdk-test-" + suffix,
		BuildID: "v1-" + suffix, ManifestDigest: manifestDigest,
	}

	temporalClient, err := client.Dial(client.Options{HostPort: config.TemporalAddress, Namespace: config.TemporalNamespace})
	if err != nil {
		t.Fatal(err)
	}
	defer temporalClient.Close()

	stopFirst := startWorkerRuntime(t, config, finish, workflowDefinition)
	waitForWorkerVersion(t, temporalClient, config)
	workflowID := "org-sdk-runtime-test-" + suffix
	run, err := temporalClient.ExecuteWorkflow(context.Background(), client.StartWorkflowOptions{
		ID: workflowID, TaskQueue: config.TaskQueue,
		VersioningOverride: &client.PinnedVersioningOverride{Version: worker.WorkerDeploymentVersion{DeploymentName: config.DeploymentName, BuildID: config.BuildID}},
	}, workflowDefinition.Name, "hello")
	if err != nil {
		stopFirst()
		t.Fatal(err)
	}
	waitForProjection(t, temporalClient, workflowID, run.GetRunID(), func(projection orgsdk.Projection) bool {
		return len(projection.Nodes) == 1 && projection.Nodes[0].Status == orgsdk.NodeStatusWaitingForUser
	})
	if activityCalls.Load() != 0 {
		stopFirst()
		t.Fatalf("idle wait scheduled %d Activity calls", activityCalls.Load())
	}
	stopFirst()

	nodeID := orgsdk.RuntimeNodeID(definition.Name, "", "approval", "singleton")
	if err := temporalClient.SignalWorkflow(context.Background(), workflowID, run.GetRunID(), orgsdk.ReservedActionSignal, orgsdk.ActionEnvelope{OperationID: "op-restart", NodeID: nodeID, Action: "confirm"}); err != nil {
		t.Fatal(err)
	}
	stopSecond := startWorkerRuntime(t, config, finish, workflowDefinition)
	defer stopSecond()
	waitForWorkerVersion(t, temporalClient, config)
	var result string
	resultCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := run.Get(resultCtx, &result); err != nil {
		t.Fatal(err)
	}
	if result != "hello-confirmed" || activityCalls.Load() != 1 {
		t.Fatalf("result=%q Activity calls=%d", result, activityCalls.Load())
	}
	projection := waitForProjection(t, temporalClient, workflowID, run.GetRunID(), func(projection orgsdk.Projection) bool {
		return projection.Status == "completed"
	})
	if len(projection.ActionOutcomes) != 1 || projection.ActionOutcomes[0].State != orgsdk.ActionOutcomeAccepted || projection.RecentEvents[len(projection.RecentEvents)-1].Type != orgsdk.EventGraphCompleted {
		t.Fatalf("terminal projection = %#v", projection)
	}
}

func waitForWorkerVersion(t *testing.T, temporalClient client.Client, config orgsdk.WorkerConfig) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		description, err := temporalClient.WorkerDeploymentClient().GetHandle(config.DeploymentName).DescribeVersion(
			context.Background(), client.WorkerDeploymentDescribeVersionOptions{BuildID: config.BuildID},
		)
		if err == nil {
			for _, queue := range description.Info.TaskQueuesInfos {
				if queue.Name == config.TaskQueue && queue.Type == client.TaskQueueTypeWorkflow {
					return
				}
			}
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("Worker version did not start polling: %v", lastErr)
}

func startWorkerRuntime(t *testing.T, config orgsdk.WorkerConfig, registrations ...orgsdk.Registration) func() {
	t.Helper()
	runtime, err := orgsdk.NewWorkerRuntime(config, registrations...)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	return func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Worker runtime stopped with error: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("Worker runtime did not stop")
		}
	}
}

func waitForProjection(t *testing.T, temporalClient client.Client, workflowID, runID string, predicate func(orgsdk.Projection) bool) orgsdk.Projection {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var last orgsdk.Projection
	var lastErr error
	for time.Now().Before(deadline) {
		value, err := temporalClient.QueryWorkflow(context.Background(), workflowID, runID, orgsdk.ReservedProjectionQuery)
		if err == nil {
			err = value.Get(&last)
		}
		if err == nil && predicate(last) {
			return last
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("projection condition timed out: last=%#v error=%v", last, lastErr)
	return orgsdk.Projection{}
}
