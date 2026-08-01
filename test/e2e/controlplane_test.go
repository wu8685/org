package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/wu8685/org/internal/console"
	"github.com/wu8685/org/internal/domain"
	"github.com/wu8685/org/internal/service"
)

func TestLocalControlPlaneAcceptance(t *testing.T) {
	if os.Getenv("ORG_E2E") != "1" {
		t.Skip("set ORG_E2E=1 or run make e2e-local")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	run := newAcceptanceRun(t, ctx)
	t.Cleanup(run.cleanup)

	versionA := run.buildAndLoadSample("a")
	versionB := run.buildAndLoadSample("b")
	controlPlane := run.controlPlane()
	authA, authB := run.auth("a"), run.auth("b")

	deploymentA, err := controlPlane.PublishVersion(ctx, authA, run.workerVersionRequest(versionA))
	if err != nil {
		t.Fatalf("deploy version A: %v", err)
	}
	assertReadyDeployment(t, deploymentA, versionA)

	deploymentB, err := controlPlane.PublishVersion(ctx, authA, run.workerVersionRequest(versionB))
	if err != nil {
		t.Fatalf("deploy version B: %v", err)
	}
	assertReadyDeployment(t, deploymentB, versionB)

	tenantBDeployment, err := controlPlane.PublishVersion(ctx, authB, run.workerVersionRequest(versionA))
	if err != nil {
		t.Fatalf("deploy same workerName/version for Tenant B: %v", err)
	}
	assertReadyDeployment(t, tenantBDeployment, versionA)
	if deploymentA.TaskQueue == tenantBDeployment.TaskQueue || deploymentA.WorkerDeployment == tenantBDeployment.WorkerDeployment || deploymentA.KubernetesDeployment == tenantBDeployment.KubernetesDeployment {
		t.Fatalf("Tenant runtime names collided: A=%+v B=%+v", deploymentA, tenantBDeployment)
	}

	currentName := "current-" + run.id
	current, err := controlPlane.Start(ctx, authA, service.StartRequest{WorkerName: run.workerName, Workflow: "HelloWorkflow", Input: helloInput(currentName)})
	if err != nil {
		t.Fatalf("start Current invocation: %v", err)
	}
	run.trackInvocation(authA, current.ID)
	currentView := waitForCompletedProjection(t, ctx, controlPlane, authA, current.ID)
	assertExecutionVersion(t, currentView, versionB.version, currentName)

	historicalName := "historical-" + run.id
	historical, err := controlPlane.Start(ctx, authA, service.StartRequest{WorkerName: run.workerName, Workflow: "HelloWorkflow", WorkerVersion: versionA.version, Input: helloInput(historicalName)})
	if err != nil {
		t.Fatalf("start historical invocation: %v", err)
	}
	run.trackInvocation(authA, historical.ID)
	if historical.ID == current.ID {
		t.Fatal("independent invocations reused a Workflow ID")
	}
	historicalView := waitForCompletedProjection(t, ctx, controlPlane, authA, historical.ID)
	assertExecutionVersion(t, historicalView, versionA.version, historicalName)
	verifyConsoleHTTPAcceptance(t, controlPlane, run.tenants["a"], authA, current.ID, historical.ID, versionA.version, versionB.version)

	tenantBName := "tenant-b-" + run.id
	tenantBInvocation, err := controlPlane.Start(ctx, authB, service.StartRequest{WorkerName: run.workerName, Workflow: "HelloWorkflow", Input: helloInput(tenantBName)})
	if err != nil {
		t.Fatalf("start Tenant B invocation: %v", err)
	}
	run.trackInvocation(authB, tenantBInvocation.ID)
	tenantBView := waitForCompletedProjection(t, ctx, controlPlane, authB, tenantBInvocation.ID)
	assertExecutionVersion(t, tenantBView, versionA.version, tenantBName)
	if _, err := controlPlane.GetInvocation(ctx, authA, tenantBInvocation.ID); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("Tenant A cross-read of Tenant B invocation = %v", err)
	}
	if len(run.store.WorkerVersions(run.tenants["a"].ID, run.workerName)) != 2 || len(run.store.WorkerVersions(run.tenants["b"].ID, run.workerName)) != 1 {
		t.Fatalf("tenant-qualified deployment store mismatch")
	}
}

func verifyConsoleHTTPAcceptance(t *testing.T, controlPlane *service.ControlPlane, tenant domain.Tenant, auth service.AuthenticatedContext, currentRunID, historicalRunID, historicalVersion, currentVersion string) {
	t.Helper()
	handler := console.New(console.Config{Authenticator: console.StaticAuthenticator{Identity: console.Identity{Auth: auth, TenantDisplayName: tenant.DisplayName, CSRFToken: "e2e-csrf"}}, ControlPlane: controlPlane})
	checks := []struct {
		path  string
		wants []string
	}{
		{"/api/v1/workers", []string{`"workerName":"hello-worker"`}},
		{"/api/v1/workflows", []string{`"current":true`, `"workerVersion":"` + currentVersion + `"`}},
		{"/api/v1/runs/" + currentRunID, []string{`"selectedVersion":"` + currentVersion + `"`, `"semanticProjection":`, `"runStatus":"completed"`}},
		{"/api/v1/runs/" + historicalRunID, []string{`"selectedVersion":"` + historicalVersion + `"`, `"description":"Hello Worker release ` + historicalVersion + `."`}},
		{"/runs/" + currentRunID, []string{`data-page="run"`, `data-dag-canvas`, `data-dag-list`}},
	}
	for _, check := range checks {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, check.path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("Console GET %s status=%d body=%s", check.path, response.Code, response.Body.String())
		}
		for _, want := range check.wants {
			if !strings.Contains(response.Body.String(), want) {
				t.Fatalf("Console GET %s missing %q: %s", check.path, want, response.Body.String())
			}
		}
		for _, forbidden := range []string{"secret-task-queue", "secret-worker-deployment", `"scope"`} {
			if strings.Contains(response.Body.String(), forbidden) {
				t.Fatalf("Console GET %s leaked %q", check.path, forbidden)
			}
		}
	}
}

func helloInput(name string) json.RawMessage {
	value, _ := json.Marshal(map[string]string{"name": name})
	return value
}

func assertReadyDeployment(t *testing.T, deployment domain.WorkerVersion, image sampleImage) {
	t.Helper()
	if deployment.State != domain.WorkerVersionReady || !deployment.Health.KubernetesReady || !deployment.Health.WorkerPolling {
		t.Fatalf("deployment is not ready: %#v", deployment)
	}
	if deployment.Image != image.digestReference {
		t.Fatalf("recorded image = %q, want %q", deployment.Image, image.digestReference)
	}
	if deployment.Source.CIReference == "" || !strings.HasPrefix(deployment.Actor, "e2e-operator-") {
		t.Fatalf("missing provenance/audit data: %#v", deployment)
	}
}
