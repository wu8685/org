package console

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wu8685/org/internal/domain"
	"github.com/wu8685/org/internal/service"
	"github.com/wu8685/org/sdk/orgsdk"
)

func TestWorkerAndWorkflowReadAPIsReturnTenantCatalogWithoutRoutingInternals(t *testing.T) {
	now := time.Now().UTC()
	version := consoleVersion(now)
	backend := &stubControlPlane{
		workers:    []domain.Worker{{TenantID: "tenant-a", TenantSlug: "alpha", Name: "hello-worker", CurrentVersion: "v1"}},
		workerView: service.WorkerView{Worker: domain.Worker{Name: "hello-worker", CurrentVersion: "v1"}, Versions: []domain.WorkerVersion{version}},
		versions:   []domain.WorkerVersion{version}, version: version,
		workflows: []service.WorkflowCatalogItem{{WorkerName: "hello-worker", WorkerVersion: "v1", VersionDescription: version.Description, Current: true, Workflow: version.Metadata.Workflows[0]}},
	}
	handler := New(Config{Authenticator: stubAuthenticator{identity: testIdentity()}, ControlPlane: backend})

	checks := []struct {
		path string
		want string
	}{
		{"/api/v1/workers", `"workerName":"hello-worker"`},
		{"/api/v1/workers/hello-worker", `"currentVersion":"v1"`},
		{"/api/v1/workers/hello-worker/versions", `"version":"v1"`},
		{"/api/v1/workflows", `"name":"HelloWorkflow"`},
	}
	for _, check := range checks {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, check.path, nil))
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), check.want) {
			t.Fatalf("GET %s status=%d body=%s", check.path, response.Code, response.Body.String())
		}
		for _, forbidden := range []string{"secret-task-queue", "secret-worker-deployment", "secret-kube-deployment", "temporalNamespace", "kubernetesNamespace", `"scope"`} {
			if strings.Contains(response.Body.String(), forbidden) {
				t.Fatalf("GET %s leaked %q: %s", check.path, forbidden, response.Body.String())
			}
		}
	}
}

func TestOverviewAPIReturnsTenantScopedProductSummary(t *testing.T) {
	backend := &stubControlPlane{overview: service.Overview{
		TenantID: "tenant-a", TenantStatus: domain.TenantActive,
		Workers: service.CountSummary{Total: 2}, Versions: service.VersionCountSummary{Ready: 1, Failed: 1}, Runs: service.CountSummary{Total: 3},
		QuotaPolicy: domain.DefaultTenantQuotaPolicy(), QuotaUsage: service.QuotaUsage{ConcurrentRuns: 1},
	}}
	handler := New(Config{Authenticator: stubAuthenticator{identity: testIdentity()}, ControlPlane: backend})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"concurrentRuns":1`) || !strings.Contains(response.Body.String(), `"total":2`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	for _, forbidden := range []string{"temporalNamespace", "kubernetesNamespace", `"scope"`} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Fatalf("overview leaked %q: %s", forbidden, response.Body.String())
		}
	}
}

func TestWorkerVersionDetailExposesReadOnlyContractAndProbeVerification(t *testing.T) {
	backend := &stubControlPlane{version: consoleVersion(time.Now().UTC())}
	handler := New(Config{Authenticator: stubAuthenticator{identity: testIdentity()}, ControlPlane: backend})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/workers/hello-worker/versions/v1", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, want := range []string{`"contract":`, `"contractVerification":`, `"registration":{"registeredAt":`, `"status":"accepted"`, `"probe":{"status":"verified"`, `"manifestDigest":"sha256:`, `"revision":3`} {
		if !strings.Contains(body, want) {
			t.Fatalf("version detail missing %s: %s", want, body)
		}
	}
	for _, forbidden := range []string{`"metadata":`, "secret-task-queue", "secret-worker-deployment", `"scope"`} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("version detail leaked %q: %s", forbidden, body)
		}
	}
	if response.Header().Get("ETag") != `"version-v1-r3"` {
		t.Fatalf("ETag = %q", response.Header().Get("ETag"))
	}
}

func TestRunReadAPIsReturnValidatedProjectionActionLedgerAndConditionalETag(t *testing.T) {
	projection := orgsdk.Projection{
		ContractVersion: orgsdk.ContractVersion, WorkflowName: "HelloWorkflow", WorkerVersion: "v1", Revision: 7, Status: "running",
		Nodes:          []orgsdk.NodeProjection{{RuntimeNodeID: "prepare-aaaaaaaaaaaaaaaa", TemplateID: "prepare", Label: "Prepare", Status: orgsdk.NodeStatusRunning}},
		CurrentNodeIDs: []string{"prepare-aaaaaaaaaaaaaaaa"}, AllowedActions: []orgsdk.AllowedAction{},
	}
	backend := &stubControlPlane{
		runs: []domain.Invocation{{ID: "run-1", WorkerName: "hello-worker", Workflow: "HelloWorkflow", SelectedVersion: "v1"}},
		invocationView: service.InvocationView{
			Invocation:    domain.Invocation{ID: "run-1", WorkerName: "hello-worker", Workflow: "HelloWorkflow", SelectedVersion: "v1", TaskQueue: "secret-task-queue", TemporalWorkflowID: "secret-workflow-id"},
			WorkerVersion: domain.WorkerVersion{WorkerName: "hello-worker", Version: "v1", Description: "First release", WorkerDeployment: "secret-worker-deployment"},
			Execution:     service.ExecutionState{Status: "running"}, SemanticProjection: &projection,
		},
		actions: []domain.ActionOperation{{ID: "act-1", RunID: "run-1", RuntimeNodeID: "prepare-aaaaaaaaaaaaaaaa", Action: "confirm", OperationID: "op-1", State: domain.ActionDeliveryDelivered}},
	}
	handler := New(Config{Authenticator: stubAuthenticator{identity: testIdentity()}, ControlPlane: backend})

	list := httptest.NewRecorder()
	handler.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/v1/runs?workerName=hello-worker", nil))
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"id":"run-1"`) || !strings.Contains(list.Body.String(), `"executionStatus":"running"`) {
		t.Fatalf("runs status=%d body=%s", list.Code, list.Body.String())
	}
	filtered := httptest.NewRecorder()
	handler.ServeHTTP(filtered, httptest.NewRequest(http.MethodGet, "/api/v1/runs?status=completed", nil))
	if filtered.Code != http.StatusOK || strings.Contains(filtered.Body.String(), `"id":"run-1"`) {
		t.Fatalf("status filter status=%d body=%s", filtered.Code, filtered.Body.String())
	}

	detail := httptest.NewRecorder()
	handler.ServeHTTP(detail, httptest.NewRequest(http.MethodGet, "/api/v1/runs/run-1", nil))
	if detail.Code != http.StatusOK || detail.Header().Get("ETag") != `"projection-r7"` {
		t.Fatalf("detail status=%d etag=%q body=%s", detail.Code, detail.Header().Get("ETag"), detail.Body.String())
	}
	for _, want := range []string{`"semanticProjection":`, `"projectionRevision":7`, `"actionOperations":`, `"state":"delivered"`, `"description":"First release"`} {
		if !strings.Contains(detail.Body.String(), want) {
			t.Fatalf("detail missing %s: %s", want, detail.Body.String())
		}
	}
	for _, forbidden := range []string{"secret-task-queue", "secret-worker-deployment", "secret-workflow-id", `"scope"`} {
		if strings.Contains(detail.Body.String(), forbidden) {
			t.Fatalf("detail leaked %q: %s", forbidden, detail.Body.String())
		}
	}

	conditionalRequest := httptest.NewRequest(http.MethodGet, "/api/v1/runs/run-1", nil)
	conditionalRequest.Header.Set("If-None-Match", `"projection-r7"`)
	conditional := httptest.NewRecorder()
	handler.ServeHTTP(conditional, conditionalRequest)
	if conditional.Code != http.StatusNotModified || conditional.Body.Len() != 0 {
		t.Fatalf("conditional status=%d body=%s", conditional.Code, conditional.Body.String())
	}
}

func TestRoutinesAPIIsNotExposed(t *testing.T) {
	handler := New(Config{Authenticator: stubAuthenticator{identity: testIdentity()}, ControlPlane: &stubControlPlane{}})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/routines", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func consoleVersion(now time.Time) domain.WorkerVersion {
	manifest := orgsdk.Manifest{
		ContractVersion: orgsdk.ContractVersion, ProjectionEventVersion: orgsdk.ProjectionEventVersion, DynamicNodeIDVersion: orgsdk.DynamicNodeIDVersion,
		SDK:       orgsdk.SDKManifest{ModuleVersion: orgsdk.SDKModuleVersion, RuntimeProtocolVersion: orgsdk.RuntimeProtocolVersion},
		Workflows: []orgsdk.WorkflowManifest{{Name: "HelloWorkflow", VersioningBehavior: "pinned", ProjectionQuery: orgsdk.ReservedProjectionQuery, NodeTemplates: []orgsdk.NodeTemplate{{ID: "prepare", Label: "Prepare", Type: orgsdk.NodeTypeSemantic, Cardinality: orgsdk.CardinalitySingleton}}, RuntimeBounds: orgsdk.RuntimeBounds{MaxRuntimeNodes: 4, MaxProjectionBytes: 8192}}},
	}
	encoded, _ := json.Marshal(manifest)
	var metadata domain.WorkerMetadata
	_ = json.Unmarshal(encoded, &metadata)
	return domain.WorkerVersion{
		ID: "version-id", TenantID: "tenant-a", TenantSlug: "alpha", WorkerName: "hello-worker", Version: "v1", Description: "First release", Revision: 3,
		Image: "registry.example.com/hello@sha256:" + strings.Repeat("a", 64), ManifestDigest: "sha256:" + strings.Repeat("b", 64), Metadata: metadata,
		Runtime: domain.RuntimeSpec{CPU: "100m", Memory: "128Mi", ServiceAccount: "secret-kube-service-account"}, Source: domain.SourceProvenance{Repository: "https://example.com/repo", Commit: strings.Repeat("c", 12), CIReference: "ci-1"},
		TaskQueue: "secret-task-queue", WorkerDeployment: "secret-worker-deployment", KubernetesDeployment: "secret-kube-deployment",
		State: domain.WorkerVersionReady, RegistrationStatus: domain.BootstrapRegistrationAccepted, RegisteredAt: &now, Health: domain.WorkerVersionHealth{KubernetesReady: true, WorkerPolling: true}, Current: true, Actor: "user-a", CreatedAt: now, UpdatedAt: now,
	}
}
