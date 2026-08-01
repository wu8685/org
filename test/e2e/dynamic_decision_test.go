package e2e_test

import (
	"context"
	"encoding/json"
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

var dynamicDigestReferencePattern = regexp.MustCompile(`org\.local/dynamic-decision-worker@sha256:[0-9a-f]{64}`)
var dynamicTagPattern = regexp.MustCompile(`org\.local/dynamic-decision-worker:[A-Za-z0-9_.-]+`)

func TestLocalDynamicDecisionAcceptance(t *testing.T) {
	if os.Getenv("ORG_E2E") != "1" {
		t.Skip("set ORG_E2E=1 or run make dynamic-e2e-local")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	run := newAcceptanceRun(t, ctx)
	run.workerName = "dynamic-decision-worker"
	t.Cleanup(run.cleanup)

	image := run.buildAndLoadDynamicSample("a")
	controlPlane := run.controlPlane()
	auth := run.auth("a")
	handler := console.New(console.Config{Authenticator: console.StaticAuthenticator{Identity: console.Identity{Auth: auth, TenantDisplayName: run.tenants["a"].DisplayName, CSRFToken: "e2e-csrf"}}, ControlPlane: controlPlane})
	deployment, err := controlPlane.PublishVersion(ctx, auth, run.dynamicWorkerVersionRequest(image))
	if err != nil {
		t.Fatalf("deploy dynamic decision version: %v", err)
	}
	deployment = run.waitForReadyVersion(auth, run.workerName, image.version)
	assertReadyDeployment(t, deployment, image)

	for _, test := range []struct{ mode, selected, skipped string }{
		{mode: "concise", selected: "concise-branch", skipped: "detailed-branch"},
		{mode: "detailed", selected: "detailed-branch", skipped: "concise-branch"},
	} {
		t.Run(test.mode, func(t *testing.T) {
			input, _ := json.Marshal(map[string]string{"mode": test.mode, "subject": "release notes"})
			invocation, err := controlPlane.Start(ctx, auth, service.StartRequest{WorkerName: run.workerName, Workflow: "DynamicDecisionWorkflow", Input: input})
			if err != nil {
				t.Fatalf("start %s route: %v", test.mode, err)
			}
			run.trackInvocation(auth, invocation.ID)
			waitForProjectionNode(t, ctx, controlPlane, auth, invocation.ID, "determine-route", orgsdk.NodeStatusRunning)
			waitForDynamicSelectedRunningAndSkipped(t, ctx, controlPlane, auth, invocation.ID, test.selected, test.skipped)
			waitForProjectionNode(t, ctx, controlPlane, auth, invocation.ID, "finalize", orgsdk.NodeStatusRunning)
			view := waitForDynamicCompletedProjection(t, ctx, controlPlane, auth, invocation.ID, test.selected, test.skipped)
			assertDynamicDecisionResult(t, view, image.version, test.mode)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/runs/"+invocation.ID, nil))
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"skipped"`) || !strings.Contains(response.Body.String(), `"reasonCode":"route-not-selected"`) || !strings.Contains(response.Body.String(), `"dependencies":`) {
				t.Fatalf("Console dynamic projection status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func waitForDynamicSelectedRunningAndSkipped(t *testing.T, ctx context.Context, control *service.ControlPlane, auth service.AuthenticatedContext, runID, selectedTemplate, skippedTemplate string) service.InvocationView {
	t.Helper()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		view, err := control.GetInvocation(ctx, auth, runID)
		if err == nil && view.SemanticProjection != nil {
			var selected, skipped orgsdk.NodeProjection
			for _, node := range view.SemanticProjection.Nodes {
				switch node.TemplateID {
				case selectedTemplate:
					selected = node
				case skippedTemplate:
					skipped = node
				}
			}
			if selected.Status == orgsdk.NodeStatusRunning && skipped.Status == orgsdk.NodeStatusSkipped && skipped.ReasonCode == "route-not-selected" {
				return view
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for selected running and unselected skipped: %v (last err: %v)", ctx.Err(), err)
		case <-ticker.C:
		}
	}
}

func (r *acceptanceRun) buildAndLoadDynamicSample(suffix string) sampleImage {
	r.t.Helper()
	version := "dynamic-e2e-" + r.id + "-" + suffix
	commit := strings.Repeat("d", 12)
	cmd := exec.CommandContext(r.ctx, "make", "-C", filepath.Join(r.root, "samples", "dynamic-decision"), "kind-load", "VERSION="+version, "COMMIT="+commit)
	cmd.Env = os.Environ()
	output, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("build/load dynamic sample: %v\n%s", err, output)
	}
	digest := dynamicDigestReferencePattern.FindString(string(output))
	tag := dynamicTagPattern.FindString(string(output))
	if digest == "" || tag == "" {
		r.t.Fatalf("Dynamic Decision Sample kind-load did not print tag and digest reference:\n%s", output)
	}
	image := sampleImage{version: version, tag: tag, digestReference: digest, commit: commit}
	r.images = append(r.images, image)
	r.t.Logf("loaded dynamic sample version=%s image=%s", version, digest)
	return image
}

func (r *acceptanceRun) dynamicWorkerVersionRequest(image sampleImage) domain.WorkerVersionRequest {
	r.t.Helper()
	return domain.WorkerVersionRequest{
		WorkerName: r.workerName, Description: "Dynamic decision release " + image.version + ".",
		Image: image.digestReference, Version: image.version,
		Runtime: domain.RuntimeSpec{CPU: "100m", Memory: "128Mi", ServiceAccount: "dynamic-decision-worker"},
		Source:  domain.SourceProvenance{Repository: "https://local.test/org/samples/dynamic-decision", Branch: "e2e", Commit: image.commit, CIReference: "local-e2e-" + r.id},
	}
}

func waitForDynamicCompletedProjection(t *testing.T, ctx context.Context, control *service.ControlPlane, auth service.AuthenticatedContext, runID, selectedTemplate, skippedTemplate string) service.InvocationView {
	t.Helper()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		view, err := control.GetInvocation(ctx, auth, runID)
		if err == nil && view.Execution.Status == "completed" && view.SemanticProjection != nil && view.SemanticProjection.Status == "completed" {
			var selected, skipped, finalize orgsdk.NodeProjection
			for _, node := range view.SemanticProjection.Nodes {
				switch node.TemplateID {
				case selectedTemplate:
					selected = node
				case skippedTemplate:
					skipped = node
				case "finalize":
					finalize = node
				}
			}
			if len(view.SemanticProjection.Nodes) != 4 || selected.Status != orgsdk.NodeStatusCompleted || skipped.Status != orgsdk.NodeStatusSkipped || skipped.ReasonCode != "route-not-selected" || finalize.Status != orgsdk.NodeStatusCompleted || len(finalize.Dependencies) != 2 {
				t.Fatalf("dynamic projection = %#v", view.SemanticProjection)
			}
			return view
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for dynamic decision completion: %v (last err: %v)", ctx.Err(), err)
		case <-ticker.C:
		}
	}
}

func assertDynamicDecisionResult(t *testing.T, view service.InvocationView, wantVersion, wantRoute string) {
	t.Helper()
	if view.Invocation.SelectedVersion != wantVersion || view.WorkerVersion.Description == "" {
		t.Fatalf("selected WorkerVersion = %#v", view.WorkerVersion)
	}
	var result struct {
		Subject       string `json:"subject"`
		Route         string `json:"route"`
		Content       string `json:"content"`
		WorkerVersion string `json:"workerVersion"`
	}
	if err := json.Unmarshal([]byte(view.Execution.Result), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Subject != "release notes" || result.Route != wantRoute || result.Content == "" || result.WorkerVersion != wantVersion {
		t.Fatalf("result = %#v", result)
	}
}
