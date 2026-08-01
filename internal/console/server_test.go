package console

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wu8685/org/internal/domain"
	"github.com/wu8685/org/internal/service"
)

type stubAuthenticator struct {
	identity Identity
	err      error
}

func (a stubAuthenticator) Authenticate(*http.Request) (Identity, error) {
	return a.identity, a.err
}

type stubControlPlane struct {
	created        []service.CreateWorkerRequest
	workers        []domain.Worker
	workerView     service.WorkerView
	versions       []domain.WorkerVersion
	version        domain.WorkerVersion
	workflows      []service.WorkflowCatalogItem
	runs           []domain.Invocation
	invocationView service.InvocationView
	actions        []domain.ActionOperation
	overview       service.Overview
	published      chan domain.WorkerVersionRequest
	updatedVersion domain.WorkerVersion
	startedRun     domain.Invocation
	startRequest   service.StartRequest
	cancelledRun   string
	actionRequest  service.RunActionRequest
	actionResult   domain.ActionOperation
	actionErr      error
	err            error
	publishMu      sync.Mutex
	publishByScope map[string]domain.PublishOperation
	publishByID    map[string]domain.PublishOperation
}

func (c *stubControlPlane) CreateWorker(_ context.Context, _ service.AuthenticatedContext, request service.CreateWorkerRequest) (domain.Worker, error) {
	c.created = append(c.created, request)
	if c.err != nil {
		return domain.Worker{}, c.err
	}
	return domain.Worker{Name: request.WorkerName}, nil
}

func (c *stubControlPlane) ListWorkers(context.Context, service.AuthenticatedContext) ([]domain.Worker, error) {
	return c.workers, c.err
}
func (c *stubControlPlane) GetWorker(context.Context, service.AuthenticatedContext, string) (service.WorkerView, error) {
	return c.workerView, c.err
}
func (c *stubControlPlane) ListWorkerVersions(context.Context, service.AuthenticatedContext, string) ([]domain.WorkerVersion, error) {
	return c.versions, c.err
}
func (c *stubControlPlane) GetWorkerVersion(context.Context, service.AuthenticatedContext, string, string) (domain.WorkerVersion, error) {
	return c.version, c.err
}
func (c *stubControlPlane) ListWorkflowCatalog(context.Context, service.AuthenticatedContext) ([]service.WorkflowCatalogItem, error) {
	return c.workflows, c.err
}
func (c *stubControlPlane) ListInvocations(context.Context, service.AuthenticatedContext, service.InvocationFilter) ([]domain.Invocation, error) {
	return c.runs, c.err
}
func (c *stubControlPlane) GetInvocation(context.Context, service.AuthenticatedContext, string) (service.InvocationView, error) {
	return c.invocationView, c.err
}
func (c *stubControlPlane) ListRunActions(context.Context, service.AuthenticatedContext, string) ([]domain.ActionOperation, error) {
	return c.actions, c.err
}
func (c *stubControlPlane) GetOverview(context.Context, service.AuthenticatedContext) (service.Overview, error) {
	return c.overview, c.err
}
func (c *stubControlPlane) PublishVersion(_ context.Context, _ service.AuthenticatedContext, request domain.WorkerVersionRequest) (domain.WorkerVersion, error) {
	if c.published != nil {
		c.published <- request
	}
	if c.err != nil {
		return domain.WorkerVersion{}, c.err
	}
	result := c.version
	if result.Version == "" {
		result = domain.WorkerVersion{WorkerName: request.WorkerName, Version: request.Version, Description: request.Description, Revision: 1, VersionConfig: request.VersionConfig, State: domain.WorkerVersionReady}
	}
	return result, nil
}
func (c *stubControlPlane) ReservePublishOperation(_ context.Context, auth service.AuthenticatedContext, request service.PublishOperationReservation) (domain.PublishOperation, bool, error) {
	if request.IdempotencyKey == "" {
		return domain.PublishOperation{}, false, errors.New("Idempotency-Key must contain 1 to 200 visible ASCII characters")
	}
	c.publishMu.Lock()
	defer c.publishMu.Unlock()
	if c.publishByScope == nil {
		c.publishByScope = map[string]domain.PublishOperation{}
		c.publishByID = map[string]domain.PublishOperation{}
	}
	scope := auth.TenantID + "\x00" + auth.PrincipalID + "\x00" + request.IdempotencyKey
	if existing, ok := c.publishByScope[scope]; ok {
		if existing.PayloadDigest != request.PayloadDigest {
			return existing, false, service.ErrPublishIdempotencyConflict
		}
		return existing, false, nil
	}
	now := time.Now().UTC()
	operation := domain.PublishOperation{
		ID: "pub-test-" + string(rune('a'+len(c.publishByID))), TenantID: auth.TenantID, PrincipalID: auth.PrincipalID,
		IdempotencyKeyHash: "test-key-hash", PayloadDigest: request.PayloadDigest, WorkerName: request.WorkerName,
		Version: request.Version, State: domain.PublishOperationRunning, CreatedAt: now, UpdatedAt: now,
	}
	c.publishByScope[scope], c.publishByID[operation.ID] = operation, operation
	return operation, true, nil
}
func (c *stubControlPlane) CompletePublishOperation(_ context.Context, auth service.AuthenticatedContext, operationID string, version domain.WorkerVersion, errorCode, errorMessage string) (domain.PublishOperation, error) {
	c.publishMu.Lock()
	defer c.publishMu.Unlock()
	operation, ok := c.publishByID[operationID]
	if !ok || operation.TenantID != auth.TenantID {
		return domain.PublishOperation{}, service.ErrNotFound
	}
	operation.UpdatedAt = time.Now().UTC()
	if errorCode != "" {
		operation.State, operation.ErrorCode, operation.ErrorMessage = domain.PublishOperationFailed, errorCode, errorMessage
	} else {
		operation.State, operation.WorkerVersion = domain.PublishOperationSucceeded, &version
	}
	c.publishByID[operationID] = operation
	for scope, existing := range c.publishByScope {
		if existing.ID == operationID {
			c.publishByScope[scope] = operation
		}
	}
	return operation, nil
}
func (c *stubControlPlane) GetPublishOperation(_ context.Context, auth service.AuthenticatedContext, operationID string) (domain.PublishOperation, error) {
	c.publishMu.Lock()
	defer c.publishMu.Unlock()
	operation, ok := c.publishByID[operationID]
	if !ok || operation.TenantID != auth.TenantID {
		return domain.PublishOperation{}, service.ErrNotFound
	}
	return operation, nil
}
func (c *stubControlPlane) UpdateWorkerVersionDescription(_ context.Context, _ service.AuthenticatedContext, _, _ string, _ int64, description string) (domain.WorkerVersion, error) {
	if c.err != nil {
		return domain.WorkerVersion{}, c.err
	}
	result := c.updatedVersion
	if result.Version == "" {
		result = c.version
		result.Description, result.Revision = description, result.Revision+1
	}
	return result, nil
}
func (c *stubControlPlane) Start(_ context.Context, _ service.AuthenticatedContext, request service.StartRequest) (domain.Invocation, error) {
	c.startRequest = request
	return c.startedRun, c.err
}
func (c *stubControlPlane) Cancel(_ context.Context, _ service.AuthenticatedContext, runID string) error {
	c.cancelledRun = runID
	return c.err
}
func (c *stubControlPlane) Act(_ context.Context, _ service.AuthenticatedContext, request service.RunActionRequest) (domain.ActionOperation, error) {
	c.actionRequest = request
	return c.actionResult, c.actionErr
}
func (c *stubControlPlane) ReconcileRunActions(context.Context, service.AuthenticatedContext, string) (int, error) {
	return 0, nil
}

func TestSessionIsDerivedFromAuthenticatorAndExposesNoInfrastructureIdentity(t *testing.T) {
	backend := &stubControlPlane{}
	handler := New(Config{Authenticator: stubAuthenticator{identity: testIdentity()}, ControlPlane: backend})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/session", nil)
	request.Header.Set("X-Tenant-ID", "forged-tenant")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	session := body["session"].(map[string]any)
	tenant := session["tenant"].(map[string]any)
	if tenant["id"] != "tenant-a" || tenant["slug"] != "alpha" || session["csrfToken"] != "csrf-a" {
		t.Fatalf("session=%#v", session)
	}
	for _, forbidden := range []string{"scope", "taskQueue", "workerDeployment", "temporalNamespace", "kubernetesNamespace"} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Fatalf("session leaked %q: %s", forbidden, response.Body.String())
		}
	}
	if response.Header().Get("X-Request-ID") == "" || body["requestId"] == "" {
		t.Fatalf("missing request ID: headers=%v body=%v", response.Header(), body)
	}
}

func TestStaticDevelopmentAuthenticatorIgnoresForgedTenantHeaders(t *testing.T) {
	authenticator := StaticAuthenticator{Identity: testIdentity()}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("X-Tenant-ID", "tenant-forged")
	request.Header.Set("X-Tenant-Slug", "forged")
	identity, err := authenticator.Authenticate(request)
	if err != nil || identity.Auth.TenantID != "tenant-a" || identity.Auth.TenantSlug != "alpha" {
		t.Fatalf("identity=%#v err=%v", identity, err)
	}
}

func TestUnauthenticatedRequestUsesStableJSONErrorEnvelope(t *testing.T) {
	handler := New(Config{Authenticator: stubAuthenticator{err: service.ErrUnauthenticated}, ControlPlane: &stubControlPlane{}})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/session", nil))

	if response.Code != http.StatusUnauthorized || response.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
	var body struct {
		Error struct {
			Code, Message, RequestID string
		}
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "unauthenticated" || body.Error.Message == "" || body.Error.RequestID == "" || body.Error.RequestID != response.Header().Get("X-Request-ID") {
		t.Fatalf("error=%#v", body.Error)
	}
}

func TestMutationRequiresCSRFAndRejectsForgedTenantOrScopeFields(t *testing.T) {
	backend := &stubControlPlane{}
	handler := New(Config{Authenticator: stubAuthenticator{identity: testIdentity()}, ControlPlane: backend})

	missingCSRF := httptest.NewRecorder()
	handler.ServeHTTP(missingCSRF, httptest.NewRequest(http.MethodPost, "/api/v1/workers", strings.NewReader(`{"workerName":"hello-worker"}`)))
	if missingCSRF.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status=%d body=%s", missingCSRF.Code, missingCSRF.Body.String())
	}

	for _, body := range []string{
		`{"workerName":"hello-worker","tenantId":"tenant-b"}`,
		`{"workerName":"hello-worker","scope":"forged"}`,
	} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/workers", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-CSRF-Token", "csrf-a")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d response=%s", body, response.Code, response.Body.String())
		}
	}
	if len(backend.created) != 0 {
		t.Fatalf("forged request reached control plane: %#v", backend.created)
	}
}

func TestServiceErrorsMapToStableHTTPStatus(t *testing.T) {
	tests := []struct {
		err        error
		wantStatus int
		wantCode   string
	}{
		{service.ErrPermissionDenied, http.StatusForbidden, "permission_denied"},
		{service.ErrNotFound, http.StatusNotFound, "not_found"},
		{service.ErrWorkerVersionExists, http.StatusConflict, "worker_version_exists"},
		{service.ErrPublishIdempotencyConflict, http.StatusConflict, "idempotency_conflict"},
		{service.ErrRunIdempotencyConflict, http.StatusConflict, "run_idempotency_conflict"},
		{service.ErrConflict, http.StatusConflict, "conflict"},
		{service.ErrTenantQuotaExceeded, http.StatusTooManyRequests, "quota_exceeded"},
		{errors.New("bad input"), http.StatusBadRequest, "validation_failed"},
	}
	for _, test := range tests {
		t.Run(test.wantCode, func(t *testing.T) {
			handler := New(Config{Authenticator: stubAuthenticator{identity: testIdentity()}, ControlPlane: &stubControlPlane{err: test.err}})
			request := httptest.NewRequest(http.MethodPost, "/api/v1/workers", strings.NewReader(`{"workerName":"hello-worker"}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-CSRF-Token", "csrf-a")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus || !strings.Contains(response.Body.String(), `"code":"`+test.wantCode+`"`) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func testIdentity() Identity {
	return Identity{
		Auth: service.AuthenticatedContext{
			PrincipalID: "user-a", TenantID: "tenant-a", TenantSlug: "alpha", AuthenticationMethod: "test",
			Permissions: map[string]bool{service.PermissionWorkerRead: true, service.PermissionWorkerCreate: true},
		},
		TenantDisplayName: "Alpha Studio", CSRFToken: "csrf-a",
	}
}
