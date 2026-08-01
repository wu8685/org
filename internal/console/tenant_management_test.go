package console

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wu8685/org/internal/domain"
	"github.com/wu8685/org/internal/service"
)

func TestTenantManagementHTTPListDetailCreateAndUpdateContracts(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	tenant := domain.Tenant{ID: "tenant-studio", Slug: "studio", DisplayName: "Studio", Description: "Product workflows", Status: domain.TenantActive, QuotaPolicy: domain.DefaultTenantQuotaPolicy(), Revision: 3, CreatedAt: now, UpdatedAt: now}
	member := domain.TenantMember{TenantID: tenant.ID, PrincipalID: "user-a", PrincipalDisplayName: "User A", Role: domain.TenantRoleOwner, Revision: 2, CreatedAt: now, UpdatedAt: now}
	view := service.TenantAccessView{Tenant: tenant, Membership: member, Permissions: []string{service.PermissionTenantRead, service.PermissionTenantUpdate}, QuotaUsage: service.QuotaUsage{ConcurrentRuns: 1}, Members: []service.TenantMemberView{{TenantMember: member, Permissions: []string{service.PermissionTenantRead, service.PermissionTenantUpdate}}}, AllowedActions: map[string]bool{"update": true, "manageMembers": true}}
	backend := &stubControlPlane{tenants: []service.TenantAccessView{view}, tenantView: view, createdTenant: view, updatedTenant: view}
	handler := New(Config{Authenticator: stubAuthenticator{identity: testIdentity()}, ControlPlane: backend})

	list := httptest.NewRecorder()
	handler.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/v1/tenants", nil))
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"slug":"studio"`) || !strings.Contains(list.Body.String(), `"role":"owner"`) || !strings.Contains(list.Body.String(), `"quotaUsage"`) || list.Header().Get("ETag") == "" {
		t.Fatalf("list status=%d headers=%v body=%s", list.Code, list.Header(), list.Body.String())
	}
	detail := httptest.NewRecorder()
	handler.ServeHTTP(detail, httptest.NewRequest(http.MethodGet, "/api/v1/tenants/studio", nil))
	if detail.Code != http.StatusOK || !strings.HasPrefix(detail.Header().Get("ETag"), `"tenant-r3-`) || !strings.Contains(detail.Body.String(), `"members"`) || !strings.Contains(detail.Body.String(), `"allowedActions"`) {
		t.Fatalf("detail status=%d headers=%v body=%s", detail.Code, detail.Header(), detail.Body.String())
	}

	create := httptest.NewRequest(http.MethodPost, "/api/v1/tenants", strings.NewReader(`{"slug":"studio","displayName":"Studio","description":"Product workflows"}`))
	create.Header.Set("X-CSRF-Token", "csrf-a")
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, create)
	if created.Code != http.StatusCreated || created.Header().Get("Location") != "/tenants/studio" {
		t.Fatalf("create status=%d headers=%v body=%s", created.Code, created.Header(), created.Body.String())
	}
	forged := httptest.NewRequest(http.MethodPost, "/api/v1/tenants", strings.NewReader(`{"slug":"forged","displayName":"Forged","tenantId":"tenant-other"}`))
	forged.Header.Set("X-CSRF-Token", "csrf-a")
	forgedResponse := httptest.NewRecorder()
	handler.ServeHTTP(forgedResponse, forged)
	if forgedResponse.Code != http.StatusBadRequest {
		t.Fatalf("forged Tenant identity status=%d body=%s", forgedResponse.Code, forgedResponse.Body.String())
	}

	missingPrecondition := httptest.NewRequest(http.MethodPatch, "/api/v1/tenants/studio", strings.NewReader(`{"displayName":"Studio Team","description":"Updated","quotaPolicy":{"maxReservedCPU":"2","maxReservedMemory":"2Gi","maxActiveWorkerPods":4,"maxActiveReleases":4,"maxConcurrentRuns":16,"maxConcurrentDeployments":1}}`))
	missingPrecondition.Header.Set("X-CSRF-Token", "csrf-a")
	missingResponse := httptest.NewRecorder()
	handler.ServeHTTP(missingResponse, missingPrecondition)
	if missingResponse.Code != http.StatusPreconditionRequired {
		t.Fatalf("missing If-Match status=%d body=%s", missingResponse.Code, missingResponse.Body.String())
	}
	update := httptest.NewRequest(http.MethodPatch, "/api/v1/tenants/studio", strings.NewReader(`{"displayName":"Studio Team","description":"Updated","quotaPolicy":{"maxReservedCPU":"2","maxReservedMemory":"2Gi","maxActiveWorkerPods":4,"maxActiveReleases":4,"maxConcurrentRuns":16,"maxConcurrentDeployments":1}}`))
	update.Header.Set("X-CSRF-Token", "csrf-a")
	update.Header.Set("If-Match", `"tenant-r3"`)
	updated := httptest.NewRecorder()
	handler.ServeHTTP(updated, update)
	if updated.Code != http.StatusOK || !strings.Contains(updated.Body.String(), `"tenant"`) {
		t.Fatalf("update status=%d body=%s", updated.Code, updated.Body.String())
	}
}

func TestTenantMemberHTTPMutationsRequireCSRFRevisionAndServerRoles(t *testing.T) {
	member := domain.TenantMember{TenantID: "tenant-studio", PrincipalID: "bob", PrincipalDisplayName: "Bob", Role: domain.TenantRoleAdmin, Revision: 1}
	backend := &stubControlPlane{member: member}
	handler := New(Config{Authenticator: stubAuthenticator{identity: testIdentity()}, ControlPlane: backend})

	add := httptest.NewRequest(http.MethodPost, "/api/v1/tenants/studio/members", strings.NewReader(`{"principalId":"bob","role":"admin","permissions":{"tenant:update":true}}`))
	add.Header.Set("X-CSRF-Token", "csrf-a")
	addResponse := httptest.NewRecorder()
	handler.ServeHTTP(addResponse, add)
	if addResponse.Code != http.StatusBadRequest {
		t.Fatalf("client permissions accepted status=%d body=%s", addResponse.Code, addResponse.Body.String())
	}
	add = httptest.NewRequest(http.MethodPost, "/api/v1/tenants/studio/members", strings.NewReader(`{"principalId":"bob","role":"admin"}`))
	add.Header.Set("X-CSRF-Token", "csrf-a")
	addResponse = httptest.NewRecorder()
	handler.ServeHTTP(addResponse, add)
	if addResponse.Code != http.StatusCreated || !strings.Contains(addResponse.Body.String(), `"role":"admin"`) {
		t.Fatalf("add status=%d body=%s", addResponse.Code, addResponse.Body.String())
	}

	role := httptest.NewRequest(http.MethodPatch, "/api/v1/tenants/studio/members/bob", strings.NewReader(`{"role":"viewer"}`))
	role.Header.Set("X-CSRF-Token", "csrf-a")
	role.Header.Set("If-Match", `"member-r1"`)
	roleResponse := httptest.NewRecorder()
	handler.ServeHTTP(roleResponse, role)
	if roleResponse.Code != http.StatusOK {
		t.Fatalf("role status=%d body=%s", roleResponse.Code, roleResponse.Body.String())
	}

	remove := httptest.NewRequest(http.MethodDelete, "/api/v1/tenants/studio/members/bob", nil)
	remove.Header.Set("X-CSRF-Token", "csrf-a")
	remove.Header.Set("If-Match", `"member-r1"`)
	removeResponse := httptest.NewRecorder()
	handler.ServeHTTP(removeResponse, remove)
	if removeResponse.Code != http.StatusNoContent || backend.removedMember != "bob" {
		t.Fatalf("remove status=%d body=%s member=%q", removeResponse.Code, removeResponse.Body.String(), backend.removedMember)
	}
}
