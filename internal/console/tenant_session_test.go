package console

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/wu8685/org/internal/domain"
	"github.com/wu8685/org/internal/service"
	"github.com/wu8685/org/sdk/orgsdk"
)

type memoryTenantSelectionStore struct {
	mu       sync.Mutex
	selected map[string]string
}

func (s *memoryTenantSelectionStore) TenantSelection(sessionKey string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.selected[sessionKey]
	return value, ok
}

func (s *memoryTenantSelectionStore) SaveTenantSelection(sessionKey, tenantID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.selected[sessionKey] = tenantID
	return nil
}

type tenantRoutingControlPlane struct {
	*stubControlPlane
	mu       sync.Mutex
	lastAuth map[string]service.AuthenticatedContext
}

func (c *tenantRoutingControlPlane) ListWorkers(_ context.Context, auth service.AuthenticatedContext) ([]domain.Worker, error) {
	c.mu.Lock()
	c.lastAuth["list"] = auth
	c.mu.Unlock()
	return []domain.Worker{{TenantID: auth.TenantID, TenantSlug: auth.TenantSlug, Name: "same-worker", CurrentVersion: "version-for-" + auth.TenantSlug}}, nil
}

func (c *tenantRoutingControlPlane) Start(_ context.Context, auth service.AuthenticatedContext, request service.StartRequest) (domain.Invocation, error) {
	c.mu.Lock()
	c.lastAuth["start"] = auth
	c.mu.Unlock()
	return domain.Invocation{ID: "run-" + auth.TenantSlug, TenantID: auth.TenantID, WorkerName: request.WorkerName, Workflow: request.Workflow, SelectedVersion: "version-for-" + auth.TenantSlug}, nil
}

func (c *tenantRoutingControlPlane) Act(_ context.Context, auth service.AuthenticatedContext, request service.RunActionRequest) (domain.ActionOperation, error) {
	c.mu.Lock()
	c.lastAuth["action"] = auth
	c.mu.Unlock()
	return domain.ActionOperation{ID: "action-" + auth.TenantSlug, RunID: request.RunID, RuntimeNodeID: request.RuntimeNodeID, Action: request.Action, OperationID: request.OperationID, State: domain.ActionDeliveryDelivered}, nil
}

func TestTenantSessionListsOnlyMembershipsSwitchesServerSideAndRoutesAllOperations(t *testing.T) {
	store := &memoryTenantSelectionStore{selected: map[string]string{}}
	permissions := map[string]bool{
		service.PermissionWorkerRead: true, service.PermissionRunStart: true, service.PermissionRunRead: true,
		"run:action:confirm": true,
	}
	memberships := []TenantMembership{
		{TenantID: "tenant-a", TenantSlug: "alpha", DisplayName: "Alpha Studio", Permissions: permissions},
		{TenantID: "tenant-b", TenantSlug: "beta", DisplayName: "Beta Studio", Permissions: permissions},
	}
	authenticator, err := NewSessionAuthenticator(SessionAuthenticatorConfig{
		PrincipalID: "user-a", SessionKey: "session-a", AuthenticationMethod: "test", CSRFToken: "csrf-a",
		DefaultTenantID: "tenant-a", Memberships: memberships, SelectionStore: store,
	})
	if err != nil {
		t.Fatal(err)
	}
	projection := orgsdk.Projection{Revision: 7, Status: "waiting-for-user"}
	backend := &tenantRoutingControlPlane{
		stubControlPlane: &stubControlPlane{invocationView: service.InvocationView{Invocation: domain.Invocation{ID: "run-beta"}, SemanticProjection: &projection}},
		lastAuth:         map[string]service.AuthenticatedContext{},
	}
	handler := New(Config{Authenticator: authenticator, ControlPlane: backend})

	session := httptest.NewRecorder()
	handler.ServeHTTP(session, httptest.NewRequest(http.MethodGet, "/api/v1/session", nil))
	if session.Code != http.StatusOK || !strings.Contains(session.Body.String(), `"slug":"alpha"`) || !strings.Contains(session.Body.String(), `"slug":"beta"`) || strings.Contains(session.Body.String(), "tenant-gamma") {
		t.Fatalf("initial session status=%d body=%s", session.Code, session.Body.String())
	}

	before := httptest.NewRecorder()
	handler.ServeHTTP(before, httptest.NewRequest(http.MethodGet, "/api/v1/workers", nil))
	if before.Code != http.StatusOK || !strings.Contains(before.Body.String(), `"currentVersion":"version-for-alpha"`) {
		t.Fatalf("alpha workers status=%d body=%s", before.Code, before.Body.String())
	}

	switchRequest := httptest.NewRequest(http.MethodPost, "/api/v1/session/tenant", strings.NewReader(`{"tenantSlug":"beta"}`))
	switchRequest.Header.Set("X-CSRF-Token", "csrf-a")
	switched := httptest.NewRecorder()
	handler.ServeHTTP(switched, switchRequest)
	if switched.Code != http.StatusOK || !strings.Contains(switched.Body.String(), `"redirect":"/"`) || !strings.Contains(switched.Body.String(), `"slug":"beta"`) {
		t.Fatalf("switch status=%d body=%s", switched.Code, switched.Body.String())
	}

	after := httptest.NewRecorder()
	handler.ServeHTTP(after, httptest.NewRequest(http.MethodGet, "/api/v1/workers", nil))
	if after.Code != http.StatusOK || !strings.Contains(after.Body.String(), `"workerName":"same-worker"`) || !strings.Contains(after.Body.String(), `"currentVersion":"version-for-beta"`) || strings.Contains(after.Body.String(), "version-for-alpha") {
		t.Fatalf("beta workers status=%d body=%s", after.Code, after.Body.String())
	}

	start := httptest.NewRequest(http.MethodPost, "/api/v1/workers/same-worker/workflows/TestWorkflow/runs", strings.NewReader(`{"input":{}}`))
	start.Header.Set("X-CSRF-Token", "csrf-a")
	startResponse := httptest.NewRecorder()
	handler.ServeHTTP(startResponse, start)
	if startResponse.Code != http.StatusCreated || backend.lastAuth["start"].TenantID != "tenant-b" {
		t.Fatalf("start status=%d auth=%#v body=%s", startResponse.Code, backend.lastAuth["start"], startResponse.Body.String())
	}

	action := httptest.NewRequest(http.MethodPost, "/api/v1/runs/run-beta/nodes/approval/actions/confirm", strings.NewReader(`{"input":{}}`))
	action.Header.Set("X-CSRF-Token", "csrf-a")
	action.Header.Set("Idempotency-Key", "action-1")
	action.Header.Set("If-Match", `"projection-r7"`)
	actionResponse := httptest.NewRecorder()
	handler.ServeHTTP(actionResponse, action)
	if actionResponse.Code != http.StatusAccepted || backend.lastAuth["action"].TenantID != "tenant-b" {
		t.Fatalf("action status=%d auth=%#v body=%s", actionResponse.Code, backend.lastAuth["action"], actionResponse.Body.String())
	}

	for _, body := range []string{`{"tenantSlug":"gamma"}`, `{"tenantSlug":"alpha","tenantId":"tenant-b"}`} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/session/tenant", strings.NewReader(body))
		request.Header.Set("X-CSRF-Token", "csrf-a")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if body == `{"tenantSlug":"gamma"}` && response.Code != http.StatusForbidden {
			t.Fatalf("unauthorized switch status=%d body=%s", response.Code, response.Body.String())
		}
		if strings.Contains(body, "tenantId") && response.Code != http.StatusBadRequest {
			t.Fatalf("forged Tenant field status=%d body=%s", response.Code, response.Body.String())
		}
	}
}

func TestTenantSessionSelectionSurvivesAuthenticatorRestartAndRemovedMembershipFallsBack(t *testing.T) {
	store := &memoryTenantSelectionStore{selected: map[string]string{}}
	memberships := []TenantMembership{
		{TenantID: "tenant-a", TenantSlug: "alpha", DisplayName: "Alpha"},
		{TenantID: "tenant-b", TenantSlug: "beta", DisplayName: "Beta"},
	}
	newAuthenticator := func(available []TenantMembership) *SessionAuthenticator {
		authenticator, err := NewSessionAuthenticator(SessionAuthenticatorConfig{
			PrincipalID: "user-a", SessionKey: "session-a", AuthenticationMethod: "test", CSRFToken: "csrf-a",
			DefaultTenantID: "tenant-a", Memberships: available, SelectionStore: store,
		})
		if err != nil {
			t.Fatal(err)
		}
		return authenticator
	}
	first := newAuthenticator(memberships)
	if _, err := first.SelectTenant(httptest.NewRequest(http.MethodPost, "/", nil), "beta"); err != nil {
		t.Fatal(err)
	}
	restarted := newAuthenticator(memberships)
	identity, err := restarted.Authenticate(httptest.NewRequest(http.MethodGet, "/", nil))
	if err != nil || identity.Auth.TenantID != "tenant-b" {
		t.Fatalf("restart identity=%#v err=%v", identity, err)
	}
	removed := newAuthenticator(memberships[:1])
	identity, err = removed.Authenticate(httptest.NewRequest(http.MethodGet, "/", nil))
	if err != nil || identity.Auth.TenantID != "tenant-a" {
		t.Fatalf("removed membership fallback identity=%#v err=%v", identity, err)
	}
	if selected, ok := store.TenantSelection("session-a"); !ok || selected != "tenant-a" {
		t.Fatalf("durable fallback selection=%q exists=%v", selected, ok)
	}
}

func TestTenantSwitchRequestStillRequiresCSRF(t *testing.T) {
	store := &memoryTenantSelectionStore{selected: map[string]string{}}
	authenticator, err := NewSessionAuthenticator(SessionAuthenticatorConfig{
		PrincipalID: "user-a", SessionKey: "session-a", AuthenticationMethod: "test", CSRFToken: "csrf-a", DefaultTenantID: "tenant-a",
		Memberships: []TenantMembership{{TenantID: "tenant-a", TenantSlug: "alpha", DisplayName: "Alpha"}}, SelectionStore: store,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := New(Config{Authenticator: authenticator, ControlPlane: &stubControlPlane{}})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/session/tenant", strings.NewReader(`{"tenantSlug":"alpha"}`)))
	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func decodeSessionTenant(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Session struct {
			Tenant struct {
				ID string `json:"id"`
			} `json:"tenant"`
		} `json:"session"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Session.Tenant.ID
}
