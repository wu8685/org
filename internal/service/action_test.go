package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/wu8685/org/internal/domain"
	"github.com/wu8685/org/sdk/orgsdk"
)

func TestRunActionAuthorizesValidatesAndDeduplicatesSignal(t *testing.T) {
	executor := &fakeExecutor{queryResult: waitingProjectionJSON("approval-node", "confirm")}
	cp, auth := newTestControlPlane(t, Config{RegistryAllowlist: []string{"registry.example.com"}}, &fakeCluster{}, executor)
	auth.Permissions["run:action:confirm"] = true
	request := sdkActionWorkerVersionRequest("v1")
	if _, err := cp.PublishVersion(context.Background(), auth, request); err != nil {
		t.Fatal(err)
	}
	invocation, err := cp.Start(context.Background(), auth, StartRequest{WorkerName: request.WorkerName, Workflow: "ChargeOrder", Input: []byte("{}")})
	if err != nil {
		t.Fatal(err)
	}
	actionRequest := RunActionRequest{
		RunID: invocation.ID, RuntimeNodeID: "approval-node", Action: "confirm",
		OperationID: "op-42", Input: json.RawMessage("{\"comment\":\"approved\"}"),
	}
	first, err := cp.Act(context.Background(), auth, actionRequest)
	if err != nil {
		t.Fatal(err)
	}
	second, err := cp.Act(context.Background(), auth, actionRequest)
	if err != nil {
		t.Fatal(err)
	}
	if first.State != ActionDeliveryDelivered || second.ID != first.ID || len(executor.signals) != 1 {
		t.Fatalf("first=%#v second=%#v signals=%d", first, second, len(executor.signals))
	}
	signal := executor.signals[0]
	if signal.name != orgsdk.ReservedActionSignal {
		t.Fatalf("signal name = %q", signal.name)
	}
	var envelope GatewayActionEnvelope
	if err := json.Unmarshal(signal.input, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.OperationID != "op-42" || envelope.NodeID != "approval-node" || envelope.Action != "confirm" || envelope.Principal.TenantID != auth.TenantID || envelope.Principal.ID != auth.PrincipalID {
		t.Fatalf("envelope = %#v", envelope)
	}
	conflict := actionRequest
	conflict.Input = json.RawMessage("{\"comment\":\"different\"}")
	if _, err := cp.Act(context.Background(), auth, conflict); !errors.Is(err, ErrConflict) {
		t.Fatalf("different payload = %v", err)
	}
	audits := cp.store.Audits(auth.TenantID)
	found := false
	for _, audit := range audits {
		if audit.Action == "run.action" && audit.References["operationId"] == "op-42" && audit.References["runtimeNodeId"] == "approval-node" {
			found = true
		}
	}
	if !found {
		t.Fatalf("action audit not found: %#v", audits)
	}
}

func TestRunActionRejectsPermissionSchemaAndStaleProjectionBeforeSignal(t *testing.T) {
	executor := &fakeExecutor{queryResult: waitingProjectionJSON("approval-node", "confirm")}
	cp, auth := newTestControlPlane(t, Config{RegistryAllowlist: []string{"registry.example.com"}}, &fakeCluster{}, executor)
	request := sdkActionWorkerVersionRequest("v1")
	if _, err := cp.PublishVersion(context.Background(), auth, request); err != nil {
		t.Fatal(err)
	}
	invocation, err := cp.Start(context.Background(), auth, StartRequest{WorkerName: request.WorkerName, Workflow: "ChargeOrder", Input: []byte("{}")})
	if err != nil {
		t.Fatal(err)
	}
	actionRequest := RunActionRequest{RunID: invocation.ID, RuntimeNodeID: "approval-node", Action: "confirm", OperationID: "op-1", Input: json.RawMessage("{\"comment\":\"approved\"}")}
	if _, err := cp.Act(context.Background(), auth, actionRequest); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("missing dynamic permission = %v", err)
	}
	auth.Permissions["run:action:confirm"] = true
	actionRequest.Input = json.RawMessage("{\"unexpected\":true}")
	if _, err := cp.Act(context.Background(), auth, actionRequest); err == nil || !strings.Contains(err.Error(), "schema") {
		t.Fatalf("invalid schema = %v", err)
	}
	executor.queryResult = []byte("{\"contractVersion\":\"org.worker/v1\",\"workflowName\":\"ChargeOrder\",\"workerVersion\":\"v1\",\"runStatus\":\"running\",\"nodes\":[{\"runtimeNodeId\":\"approval-node\",\"templateId\":\"approval\",\"status\":\"completed\"}],\"currentNodeIds\":[],\"allowedActions\":[]}")
	actionRequest.Input = json.RawMessage("{\"comment\":\"approved\"}")
	if _, err := cp.Act(context.Background(), auth, actionRequest); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale action = %v", err)
	}
	if len(executor.signals) != 0 {
		t.Fatalf("rejected action sent %d signals", len(executor.signals))
	}
}

func TestRunActionStoresDeliveryUnknownWithoutClaimingAcceptance(t *testing.T) {
	executor := &fakeExecutor{queryResult: waitingProjectionJSON("approval-node", "confirm"), signalErr: errors.New("transport lost after send")}
	cp, auth := newTestControlPlane(t, Config{RegistryAllowlist: []string{"registry.example.com"}}, &fakeCluster{}, executor)
	auth.Permissions["run:action:confirm"] = true
	request := sdkActionWorkerVersionRequest("v1")
	if _, err := cp.PublishVersion(context.Background(), auth, request); err != nil {
		t.Fatal(err)
	}
	invocation, err := cp.Start(context.Background(), auth, StartRequest{WorkerName: request.WorkerName, Workflow: "ChargeOrder", Input: []byte("{}")})
	if err != nil {
		t.Fatal(err)
	}
	result, err := cp.Act(context.Background(), auth, RunActionRequest{RunID: invocation.ID, RuntimeNodeID: "approval-node", Action: "confirm", OperationID: "op-unknown", Input: json.RawMessage("{\"comment\":\"approved\"}")})
	if err == nil || result.State != ActionDeliveryUnknown {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if result.State == ActionDeliveryAccepted {
		t.Fatal("transport result was presented as Workflow acceptance")
	}
}

func TestRunActionRetryReturnsDeliveredOperationAfterNodeAdvances(t *testing.T) {
	executor := &fakeExecutor{queryResult: waitingProjectionJSON("approval-node", "confirm")}
	cp, auth := newTestControlPlane(t, Config{RegistryAllowlist: []string{"registry.example.com"}}, &fakeCluster{}, executor)
	auth.Permissions["run:action:confirm"] = true
	request := sdkActionWorkerVersionRequest("v1")
	if _, err := cp.PublishVersion(context.Background(), auth, request); err != nil {
		t.Fatal(err)
	}
	invocation, err := cp.Start(context.Background(), auth, StartRequest{WorkerName: request.WorkerName, Workflow: "ChargeOrder", Input: []byte("{}")})
	if err != nil {
		t.Fatal(err)
	}
	action := RunActionRequest{RunID: invocation.ID, RuntimeNodeID: "approval-node", Action: "confirm", OperationID: "op-replay", Input: json.RawMessage("{\"comment\":\"approved\"}")}
	first, err := cp.Act(context.Background(), auth, action)
	if err != nil {
		t.Fatal(err)
	}
	executor.queryResult = []byte("{\"contractVersion\":\"org.worker/v1\",\"workflowName\":\"ChargeOrder\",\"workerVersion\":\"v1\",\"projectionRevision\":5,\"runStatus\":\"completed\",\"nodes\":[{\"runtimeNodeId\":\"approval-node\",\"templateId\":\"approval\",\"status\":\"completed\"}],\"currentNodeIds\":[],\"allowedActions\":[]}")
	replayed, err := cp.Act(context.Background(), auth, action)
	if err != nil || replayed.ID != first.ID || replayed.State != ActionDeliveryDelivered || len(executor.signals) != 1 {
		t.Fatalf("replayed=%#v err=%v signals=%d", replayed, err, len(executor.signals))
	}
}

func TestRunActionRetryRedeliversDeliveryUnknownWithSameOperationID(t *testing.T) {
	executor := &fakeExecutor{queryResult: waitingProjectionJSON("approval-node", "confirm"), signalErr: errors.New("transport lost after send")}
	cp, auth := newTestControlPlane(t, Config{RegistryAllowlist: []string{"registry.example.com"}}, &fakeCluster{}, executor)
	auth.Permissions["run:action:confirm"] = true
	request := sdkActionWorkerVersionRequest("v1")
	if _, err := cp.PublishVersion(context.Background(), auth, request); err != nil {
		t.Fatal(err)
	}
	invocation, err := cp.Start(context.Background(), auth, StartRequest{WorkerName: request.WorkerName, Workflow: "ChargeOrder", Input: []byte("{}")})
	if err != nil {
		t.Fatal(err)
	}
	action := RunActionRequest{RunID: invocation.ID, RuntimeNodeID: "approval-node", Action: "confirm", OperationID: "op-redeliver", Input: json.RawMessage("{\"comment\":\"approved\"}")}
	first, err := cp.Act(context.Background(), auth, action)
	if err == nil || first.State != ActionDeliveryUnknown {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	executor.signalErr = nil
	retried, err := cp.Act(context.Background(), auth, action)
	if err != nil || retried.ID != first.ID || retried.State != ActionDeliveryDelivered || len(executor.signals) != 2 {
		t.Fatalf("retried=%#v err=%v signals=%d", retried, err, len(executor.signals))
	}
}

func TestReconcileRunActionsPromotesWorkflowAcceptedOutcome(t *testing.T) {
	executor := &fakeExecutor{queryResult: waitingProjectionJSON("approval-node", "confirm")}
	cp, auth := newTestControlPlane(t, Config{RegistryAllowlist: []string{"registry.example.com"}}, &fakeCluster{}, executor)
	auth.Permissions["run:action:confirm"] = true
	request := sdkActionWorkerVersionRequest("v1")
	if _, err := cp.PublishVersion(context.Background(), auth, request); err != nil {
		t.Fatal(err)
	}
	invocation, err := cp.Start(context.Background(), auth, StartRequest{WorkerName: request.WorkerName, Workflow: "ChargeOrder", Input: []byte("{}")})
	if err != nil {
		t.Fatal(err)
	}
	operation, err := cp.Act(context.Background(), auth, RunActionRequest{RunID: invocation.ID, RuntimeNodeID: "approval-node", Action: "confirm", OperationID: "op-accepted", Input: json.RawMessage("{\"comment\":\"approved\"}")})
	if err != nil {
		t.Fatal(err)
	}
	executor.queryResult = []byte("{\"contractVersion\":\"org.worker/v1\",\"workflowName\":\"ChargeOrder\",\"workerVersion\":\"v1\",\"projectionRevision\":5,\"runStatus\":\"running\",\"nodes\":[{\"runtimeNodeId\":\"approval-node\",\"templateId\":\"approval\",\"status\":\"completed\"}],\"currentNodeIds\":[],\"allowedActions\":[],\"actionOutcomes\":[{\"operationId\":\"op-accepted\",\"runtimeNodeId\":\"approval-node\",\"action\":\"confirm\",\"state\":\"accepted\"}]}")
	updated, err := cp.ReconcileRunActions(context.Background(), auth, invocation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated != 1 {
		t.Fatalf("updated = %d", updated)
	}
	got, ok := cp.store.ActionOperation(auth.TenantID, invocation.ID, "approval-node", "confirm", "op-accepted")
	if !ok || got.ID != operation.ID || got.State != ActionDeliveryAccepted {
		t.Fatalf("operation = %#v, %v", got, ok)
	}
}

func TestReconcileRunActionsTreatsWorkflowDuplicateAsAcceptedWithoutRedelivery(t *testing.T) {
	executor := &fakeExecutor{queryResult: waitingProjectionJSON("approval-node", "confirm")}
	cp, auth := newTestControlPlane(t, Config{RegistryAllowlist: []string{"registry.example.com"}}, &fakeCluster{}, executor)
	auth.Permissions["run:action:confirm"] = true
	request := sdkActionWorkerVersionRequest("v1")
	if _, err := cp.PublishVersion(context.Background(), auth, request); err != nil {
		t.Fatal(err)
	}
	invocation, err := cp.Start(context.Background(), auth, StartRequest{WorkerName: request.WorkerName, Workflow: "ChargeOrder", Input: []byte("{}")})
	if err != nil {
		t.Fatal(err)
	}
	operation, err := cp.Act(context.Background(), auth, RunActionRequest{RunID: invocation.ID, RuntimeNodeID: "approval-node", Action: "confirm", OperationID: "op-duplicate", Input: json.RawMessage("{\"comment\":\"approved\"}")})
	if err != nil {
		t.Fatal(err)
	}
	executor.queryResult = []byte("{\"contractVersion\":\"org.worker/v1\",\"workflowName\":\"ChargeOrder\",\"workerVersion\":\"v1\",\"projectionRevision\":6,\"runStatus\":\"running\",\"nodes\":[{\"runtimeNodeId\":\"approval-node\",\"templateId\":\"approval\",\"status\":\"completed\"}],\"currentNodeIds\":[],\"allowedActions\":[],\"actionOutcomes\":[{\"operationId\":\"op-duplicate\",\"runtimeNodeId\":\"approval-node\",\"action\":\"confirm\",\"state\":\"duplicate\"}]}")
	updated, err := cp.ReconcileRunActions(context.Background(), auth, invocation.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := cp.store.ActionOperation(auth.TenantID, invocation.ID, "approval-node", "confirm", "op-duplicate")
	if updated != 1 || !ok || got.ID != operation.ID || got.State != ActionDeliveryAccepted || len(executor.signals) != 1 {
		t.Fatalf("updated=%d operation=%#v exists=%v signals=%d", updated, got, ok, len(executor.signals))
	}
}

func sdkActionWorkerVersionRequest(version string) domain.WorkerVersionRequest {
	request := workerVersionRequest(version)
	request.Metadata.Workflows[0].ProjectionQuery = orgsdk.ReservedProjectionQuery
	request.Metadata.Workflows[0].Actions = []domain.ActionContract{{
		Name: "confirm", Label: "Confirm", NodeTemplateID: "approval",
		RequiredPermission: "run:action:confirm",
		InputSchema:        json.RawMessage("{\"type\":\"object\",\"required\":[\"comment\"],\"properties\":{\"comment\":{\"type\":\"string\",\"maxLength\":20}},\"additionalProperties\":false}"),
	}}
	return request
}

func waitingProjectionJSON(nodeID, action string) []byte {
	return []byte("{\"contractVersion\":\"org.worker/v1\",\"workflowName\":\"ChargeOrder\",\"workerVersion\":\"v1\",\"projectionRevision\":3,\"runStatus\":\"running\",\"nodes\":[{\"runtimeNodeId\":\"" + nodeID + "\",\"templateId\":\"approval\",\"label\":\"Approval\",\"status\":\"waiting-for-user\"}],\"currentNodeIds\":[\"" + nodeID + "\"],\"allowedActions\":[{\"runtimeNodeId\":\"" + nodeID + "\",\"name\":\"" + action + "\",\"label\":\"Confirm\"}]}")
}
