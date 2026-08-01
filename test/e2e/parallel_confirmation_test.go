package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/wu8685/org/internal/console"
	"github.com/wu8685/org/internal/domain"
	"github.com/wu8685/org/internal/service"
	"github.com/wu8685/org/sdk/orgsdk"
)

var parallelDigestReferencePattern = regexp.MustCompile(`org\.local/parallel-confirmation-worker@sha256:[0-9a-f]{64}`)
var parallelTagPattern = regexp.MustCompile(`org\.local/parallel-confirmation-worker:[A-Za-z0-9_.-]+`)

func TestLocalParallelConfirmationAcceptance(t *testing.T) {
	if os.Getenv("ORG_E2E") != "1" {
		t.Skip("set ORG_E2E=1 or run make parallel-e2e-local")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	run := newAcceptanceRun(t, ctx)
	run.workerName = "parallel-confirmation-worker"
	t.Cleanup(run.cleanup)

	image := run.buildAndLoadParallelSample("a")
	controlPlane := run.controlPlane()
	auth := run.auth("a")
	auth.Permissions["run:action:confirm"] = true

	deployment, err := controlPlane.PublishVersion(ctx, auth, run.parallelWorkerVersionRequest(image))
	if err != nil {
		t.Fatalf("deploy parallel confirmation version: %v", err)
	}
	deployment = run.waitForReadyVersion(auth, run.workerName, image.version)
	assertReadyDeployment(t, deployment, image)

	invocation, err := controlPlane.Start(ctx, auth, service.StartRequest{
		WorkerName: run.workerName, Workflow: "ParallelConfirmationWorkflow", Input: json.RawMessage(`{"subject":"release notes"}`),
	})
	if err != nil {
		t.Fatalf("start parallel confirmation: %v", err)
	}
	run.trackInvocation(auth, invocation.ID)

	waiting := waitForProjectionNode(t, ctx, controlPlane, auth, invocation.ID, "approval-gate", orgsdk.NodeStatusWaitingForUser)
	if len(waiting.SemanticProjection.Nodes) != 1 || len(waiting.SemanticProjection.AllowedActions) != 1 {
		t.Fatalf("idle projection = %#v", waiting.SemanticProjection)
	}
	approvalNode := waiting.SemanticProjection.Nodes[0]

	run.restartDeployment(deployment.KubernetesDeployment)
	operationID := "confirm-" + run.id
	handler := console.New(console.Config{Authenticator: console.StaticAuthenticator{Identity: console.Identity{Auth: auth, TenantDisplayName: run.tenants["a"].DisplayName, CSRFToken: "e2e-csrf"}}, ControlPlane: controlPlane})
	first := submitConsoleAction(t, handler, invocation.ID, approvalNode.RuntimeNodeID, operationID, waiting.SemanticProjection.Revision)
	second := submitConsoleAction(t, handler, invocation.ID, approvalNode.RuntimeNodeID, operationID, waiting.SemanticProjection.Revision)
	if first.State != service.ActionDeliveryDelivered || second.ID != first.ID {
		t.Fatalf("action delivery first=%#v second=%#v", first, second)
	}

	waitForProjectionNode(t, ctx, controlPlane, auth, invocation.ID, "build-plan", orgsdk.NodeStatusRunning)
	waitForParallelBranchesRunning(t, ctx, controlPlane, auth, invocation.ID)
	waitForProjectionNode(t, ctx, controlPlane, auth, invocation.ID, "finalize", orgsdk.NodeStatusRunning)
	completed := waitForParallelCompletedProjection(t, ctx, controlPlane, auth, invocation.ID)
	if completed.Invocation.SelectedVersion != image.version {
		t.Fatalf("selected version = %q, want %q", completed.Invocation.SelectedVersion, image.version)
	}
	poll := httptest.NewRecorder()
	handler.ServeHTTP(poll, httptest.NewRequest(http.MethodGet, "/api/v1/runs/"+invocation.ID+"/actions/"+operationID, nil))
	if poll.Code != http.StatusOK || !strings.Contains(poll.Body.String(), `"state":"accepted-by-workflow"`) {
		t.Fatalf("Console action poll status=%d body=%s", poll.Code, poll.Body.String())
	}
	operation, ok := run.store.ActionOperation(auth.TenantID, invocation.ID, approvalNode.RuntimeNodeID, "confirm", operationID)
	if !ok || operation.State != service.ActionDeliveryAccepted {
		t.Fatalf("reconciled action = %#v, exists=%v", operation, ok)
	}
	assertParallelActionAudit(t, run, auth, invocation.ID, operationID)
}

func waitForParallelBranchesRunning(t *testing.T, ctx context.Context, control *service.ControlPlane, auth service.AuthenticatedContext, runID string) service.InvocationView {
	t.Helper()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		view, err := control.GetInvocation(ctx, auth, runID)
		if err == nil && view.SemanticProjection != nil {
			running := 0
			for _, node := range view.SemanticProjection.Nodes {
				if node.TemplateID == "execute-branch" && node.Status == orgsdk.NodeStatusRunning {
					running++
				}
			}
			if running == 2 {
				return view
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for both parallel branches running: %v (last err: %v)", ctx.Err(), err)
		case <-ticker.C:
		}
	}
}

func submitConsoleAction(t *testing.T, handler http.Handler, runID, nodeID, operationID string, projectionRevision uint64) domain.ActionOperation {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/runs/"+runID+"/nodes/"+nodeID+"/actions/confirm", strings.NewReader(`{"input":{}}`))
	request.Header.Set("X-CSRF-Token", "e2e-csrf")
	request.Header.Set("Idempotency-Key", operationID)
	request.Header.Set("If-Match", fmt.Sprintf(`"projection-r%d"`, projectionRevision))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("Console action status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Operation domain.ActionOperation `json:"operation"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Operation
}

func (r *acceptanceRun) buildAndLoadParallelSample(suffix string) sampleImage {
	r.t.Helper()
	version := "parallel-e2e-" + r.id + "-" + suffix
	commit := strings.Repeat("c", 12)
	cmd := exec.CommandContext(r.ctx, "make", "-C", filepath.Join(r.root, "samples", "parallel-confirmation"), "kind-load", "VERSION="+version, "COMMIT="+commit)
	cmd.Env = os.Environ()
	output, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("build/load parallel sample: %v\n%s", err, output)
	}
	digest := parallelDigestReferencePattern.FindString(string(output))
	tag := parallelTagPattern.FindString(string(output))
	if digest == "" || tag == "" {
		r.t.Fatalf("Parallel Confirmation Sample kind-load did not print tag and digest reference:\n%s", output)
	}
	image := sampleImage{version: version, tag: tag, digestReference: digest, commit: commit}
	r.images = append(r.images, image)
	r.t.Logf("loaded parallel sample version=%s image=%s", version, digest)
	return image
}

func (r *acceptanceRun) parallelWorkerVersionRequest(image sampleImage) domain.WorkerVersionRequest {
	r.t.Helper()
	return domain.WorkerVersionRequest{
		WorkerName: r.workerName, Description: "Parallel confirmation release " + image.version + ".",
		Image: image.digestReference, Version: image.version,
		Runtime: domain.RuntimeSpec{CPU: "100m", Memory: "128Mi", ServiceAccount: "parallel-confirmation-worker"},
		Source:  domain.SourceProvenance{Repository: "https://local.test/org/samples/parallel-confirmation", Branch: "e2e", Commit: image.commit, CIReference: "local-e2e-" + r.id},
	}
}

func (r *acceptanceRun) restartDeployment(name string) {
	r.t.Helper()
	for _, replicas := range []string{"0", "1"} {
		r.command("kubectl", "--context", "kind-org", "-n", r.kubeNamespace, "scale", "deployment/"+name, "--replicas="+replicas)
		r.command("kubectl", "--context", "kind-org", "-n", r.kubeNamespace, "rollout", "status", "deployment/"+name, "--timeout=90s")
	}
}

func waitForProjectionNode(t *testing.T, ctx context.Context, control *service.ControlPlane, auth service.AuthenticatedContext, runID, templateID string, status orgsdk.NodeStatus) service.InvocationView {
	t.Helper()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	var last string
	for {
		view, err := control.GetInvocation(ctx, auth, runID)
		if err == nil && view.SemanticProjection != nil {
			for _, node := range view.SemanticProjection.Nodes {
				if node.TemplateID == templateID && node.Status == status {
					return view
				}
			}
			last = fmt.Sprintf("projection=%#v", view.SemanticProjection)
		} else {
			last = fmt.Sprintf("err=%v", err)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for %s=%s: %v (%s)", templateID, status, ctx.Err(), last)
		case <-ticker.C:
		}
	}
}

func waitForParallelCompletedProjection(t *testing.T, ctx context.Context, control *service.ControlPlane, auth service.AuthenticatedContext, runID string) service.InvocationView {
	t.Helper()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		view, err := control.GetInvocation(ctx, auth, runID)
		if err == nil && view.Execution.Status == "completed" && view.SemanticProjection != nil && view.SemanticProjection.Status == "completed" {
			counts := map[string]int{}
			var join orgsdk.NodeProjection
			for _, node := range view.SemanticProjection.Nodes {
				counts[node.TemplateID]++
				if node.Status != orgsdk.NodeStatusCompleted {
					t.Fatalf("non-completed node = %#v", node)
				}
				if node.TemplateID == "join" {
					join = node
				}
			}
			if len(view.SemanticProjection.Nodes) != 6 || counts["approval-gate"] != 1 || counts["build-plan"] != 1 || counts["execute-branch"] != 2 || counts["join"] != 1 || counts["finalize"] != 1 || len(join.Dependencies) != 2 {
				t.Fatalf("parallel projection = %#v", view.SemanticProjection)
			}
			if len(view.SemanticProjection.ActionOutcomes) != 1 || view.SemanticProjection.ActionOutcomes[0].State != orgsdk.ActionOutcomeAccepted {
				t.Fatalf("action outcomes = %#v", view.SemanticProjection.ActionOutcomes)
			}
			return view
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for parallel completion: %v (last err: %v)", ctx.Err(), err)
		case <-ticker.C:
		}
	}
}

func assertParallelActionAudit(t *testing.T, run *acceptanceRun, auth service.AuthenticatedContext, runID, operationID string) {
	t.Helper()
	for _, audit := range run.store.Audits(auth.TenantID) {
		if audit.Action == "run.action" && audit.TargetID == runID && audit.References["operationId"] == operationID && audit.TenantID == auth.TenantID {
			return
		}
	}
	t.Fatal("tenant-scoped run.action audit not found")
}
