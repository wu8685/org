package service

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wu8685/org/internal/domain"
)

func TestMemoryStoreRequiresTenantForEveryBusinessLookup(t *testing.T) {
	store := NewMemoryStore()
	tenantA := testTenant("tenant-a", "alpha")
	tenantB := testTenant("tenant-b", "beta")
	if err := store.SaveTenant(tenantA); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTenant(tenantB); err != nil {
		t.Fatal(err)
	}

	for _, tenant := range []domain.Tenant{tenantA, tenantB} {
		d := domain.WorkerVersion{ID: "same-id", TenantID: tenant.ID, TenantSlug: tenant.Slug, WorkerName: "payments-worker"}
		i := domain.Invocation{ID: "same-run", TenantID: tenant.ID, TenantSlug: tenant.Slug, WorkerName: "payments-worker", Workflow: "ChargeOrder", IdempotencyKey: "same-key"}
		if err := store.SaveWorkerVersion(tenant.ID, d); err != nil {
			t.Fatal(err)
		}
		if err := store.SaveInvocation(tenant.ID, i); err != nil {
			t.Fatal(err)
		}
	}

	if got := store.WorkerVersions(tenantA.ID, "payments-worker"); len(got) != 1 || got[0].TenantID != tenantA.ID {
		t.Fatalf("tenant A deployments leaked or missing: %#v", got)
	}
	if got, ok := store.Invocation(tenantB.ID, "same-run"); !ok || got.TenantID != tenantB.ID {
		t.Fatalf("tenant B invocation lookup failed: %#v %v", got, ok)
	}
	if got, ok := store.InvocationByIdempotency(tenantA.ID, "payments-worker", "ChargeOrder", "same-key"); !ok || got.TenantID != tenantA.ID {
		t.Fatalf("tenant A idempotency lookup failed: %#v %v", got, ok)
	}
}

func TestControlPlaneDerivesTenantFromAuthenticatedContextAndHidesCrossTenantObjects(t *testing.T) {
	store := NewMemoryStore()
	tenantA := testTenant("tenant-a", "alpha")
	tenantB := testTenant("tenant-b", "beta")
	if err := store.SaveTenant(tenantA); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTenant(tenantB); err != nil {
		t.Fatal(err)
	}
	cp := New(Config{RegistryAllowlist: []string{"registry.example.com"}}, store, &fakeCluster{}, &fakeExecutor{queryResult: []byte(`{"status":"running"}`)})
	if _, err := cp.CreateWorker(context.Background(), authFor(tenantA), CreateWorkerRequest{WorkerName: "payments-worker"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cp.CreateWorker(context.Background(), authFor(tenantB), CreateWorkerRequest{WorkerName: "payments-worker"}); err != nil {
		t.Fatal(err)
	}

	depA, err := cp.PublishVersion(context.Background(), authFor(tenantA), workerVersionRequest("v1"))
	if err != nil {
		t.Fatal(err)
	}
	depB, err := cp.PublishVersion(context.Background(), authFor(tenantB), workerVersionRequest("v1"))
	if err != nil {
		t.Fatal(err)
	}
	if depA.TaskQueue == depB.TaskQueue || depA.WorkerDeployment == depB.WorkerDeployment || depA.KubernetesDeployment == depB.KubernetesDeployment {
		t.Fatalf("tenant runtime names collided: A=%+v B=%+v", depA, depB)
	}

	invB, err := cp.Start(context.Background(), authFor(tenantB), StartRequest{WorkerName: "payments-worker", Workflow: "ChargeOrder", Description: "Tenant B private reason", Input: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cp.GetInvocation(context.Background(), authFor(tenantA), invB.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant read must be indistinguishable from missing, got %v", err)
	}
	if view, err := cp.GetInvocation(context.Background(), authFor(tenantB), invB.ID); err != nil || view.Invocation.Description != "Tenant B private reason" {
		t.Fatalf("own-Tenant Run description missing: view=%#v err=%v", view, err)
	}
	audits := store.Audits(tenantA.ID)
	if len(audits) == 0 || audits[len(audits)-1].AuthorizationResult != "allowed" || audits[len(audits)-1].Outcome != "failure" || audits[len(audits)-1].ErrorClass != "not_found" || audits[len(audits)-1].TenantID != tenantA.ID {
		t.Fatalf("cross-tenant not-found outcome not audited for caller tenant: %#v", audits)
	}

	deniedAuth := authFor(tenantA)
	delete(deniedAuth.Permissions, PermissionRunRead)
	if _, err := cp.GetInvocation(context.Background(), deniedAuth, invB.ID); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("missing permission should be denied, got %v", err)
	}
	audits = store.Audits(tenantA.ID)
	if audits[len(audits)-1].AuthorizationResult != "denied" || audits[len(audits)-1].ErrorClass != "permission_denied" {
		t.Fatalf("authorization denial not audited: %#v", audits[len(audits)-1])
	}
}

func TestStrictRequestDecodingRejectsForgeableTenantID(t *testing.T) {
	body := `{"tenantId":"tenant-b","scope":"payments","workerName":"payments-worker","workflow":"ChargeOrder","input":{}}`
	if _, err := DecodeStartRequest(bytes.NewBufferString(body)); err == nil || !strings.Contains(err.Error(), "tenantId") {
		t.Fatalf("expected forged tenantId to be rejected, got %v", err)
	}
}

func TestStrictDeploymentDecodingRejectsForgeableTenantID(t *testing.T) {
	body := `{"tenantId":"tenant-b","scope":"payments","workerName":"payments-worker"}`
	if _, err := DecodeWorkerVersionRequest(bytes.NewBufferString(body)); err == nil || !strings.Contains(err.Error(), "tenantId") {
		t.Fatalf("expected forged tenantId to be rejected, got %v", err)
	}
}

func TestStrictWorkerVersionDecodingRejectsLegacyIdentityAndServerOwnedRuntimeFields(t *testing.T) {
	for _, forbidden := range []string{`"scope":"legacy"`, `"name":"duplicate-worker"`, `"workerName":"duplicate-worker"`} {
		body := `{"workerName":"payments-worker","description":"Release note.","image":"registry.example.com/acme/payments@sha256:` + strings.Repeat("a", 64) + `","version":"v1","metadata":{` + forbidden + `,"workflows":[],"activities":[]},"runtime":{"cpu":"1","memory":"1Gi"},"source":{}}`
		if _, err := DecodeWorkerVersionRequest(bytes.NewBufferString(body)); err == nil {
			t.Fatalf("metadata field %s should be rejected", forbidden)
		}
	}
	body := `{"workerName":"payments-worker","description":"Release note.","metadata":{"workflows":[],"activities":[]},"runtime":{"cpu":"1","memory":"1Gi","serviceAccount":"forged"}}`
	if _, err := DecodeWorkerVersionRequest(bytes.NewBufferString(body)); err == nil || !strings.Contains(err.Error(), "serviceAccount") {
		t.Fatalf("server-owned serviceAccount should be rejected: %v", err)
	}
}

func TestSuspendedTenantBlocksMutationsButAllowsReadAndAdminCancel(t *testing.T) {
	store := NewMemoryStore()
	tenant := testTenant("tenant-suspended", "suspended")
	if err := store.SaveTenant(tenant); err != nil {
		t.Fatal(err)
	}
	executor := &fakeExecutor{queryResult: []byte(`{"status":"running"}`)}
	cp := New(Config{RegistryAllowlist: []string{"registry.example.com"}}, store, &fakeCluster{}, executor)
	auth := authFor(tenant)
	if _, err := cp.CreateWorker(context.Background(), auth, CreateWorkerRequest{WorkerName: "payments-worker"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cp.PublishVersion(context.Background(), auth, workerVersionRequest("v1")); err != nil {
		t.Fatal(err)
	}
	invocation, err := cp.Start(context.Background(), auth, StartRequest{WorkerName: "payments-worker", Workflow: "ChargeOrder", Input: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	tenant.Status = domain.TenantSuspended
	if err := store.SaveTenant(tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := cp.Start(context.Background(), auth, StartRequest{WorkerName: "payments-worker", Workflow: "ChargeOrder"}); !errors.Is(err, ErrTenantSuspended) {
		t.Fatalf("suspended start = %v", err)
	}
	if err := cp.Signal(context.Background(), auth, invocation.ID, "anything", nil); !errors.Is(err, ErrTenantSuspended) {
		t.Fatalf("suspended signal = %v", err)
	}
	view, err := cp.GetInvocation(context.Background(), auth, invocation.ID)
	if err != nil {
		t.Fatalf("suspended tenant read should remain available: %v", err)
	}
	if view.Projection.TenantID != tenant.ID || view.Projection.TenantSlug != tenant.Slug {
		t.Fatalf("projection is not tenant-qualified: %#v", view.Projection)
	}
	if err := cp.Cancel(context.Background(), auth, invocation.ID); !errors.Is(err, ErrTenantSuspended) {
		t.Fatalf("non-admin suspended cancel = %v", err)
	}
	auth.Permissions[PermissionTenantAdmin] = true
	if err := cp.Cancel(context.Background(), auth, invocation.ID); err != nil {
		t.Fatalf("admin cancel of suspended pinned run: %v", err)
	}
}

func TestAuditListingIsTenantScopedAndAuthorized(t *testing.T) {
	store := NewMemoryStore()
	tenant := testTenant("tenant-audit", "audit")
	if err := store.SaveTenant(tenant); err != nil {
		t.Fatal(err)
	}
	cp := New(Config{RegistryAllowlist: []string{"registry.example.com"}}, store, &fakeCluster{}, &fakeExecutor{})
	auth := authFor(tenant)
	if _, err := cp.CreateWorker(context.Background(), auth, CreateWorkerRequest{WorkerName: "payments-worker"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cp.PublishVersion(context.Background(), auth, workerVersionRequest("v1")); err != nil {
		t.Fatal(err)
	}
	if _, err := cp.ListAudits(context.Background(), auth); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("audit listing without permission = %v", err)
	}
	auth.Permissions[PermissionAuditRead] = true
	audits, err := cp.ListAudits(context.Background(), auth)
	if err != nil || len(audits) == 0 {
		t.Fatalf("authorized audit listing = %#v, %v", audits, err)
	}
	for _, audit := range audits {
		if audit.TenantID != tenant.ID {
			t.Fatalf("cross-tenant audit leaked: %#v", audit)
		}
	}
}

func TestWorkerVersionDescriptionPatchUsesRevisionAndPreservesRuntimeIdentity(t *testing.T) {
	store := NewMemoryStore()
	tenant := testTenant("tenant-description", "description")
	if err := store.SaveTenant(tenant); err != nil {
		t.Fatal(err)
	}
	cp := New(Config{RegistryAllowlist: []string{"registry.example.com"}}, store, &fakeCluster{}, &fakeExecutor{})
	auth := authFor(tenant)
	if _, err := cp.CreateWorker(context.Background(), auth, CreateWorkerRequest{WorkerName: "payments-worker"}); err != nil {
		t.Fatal(err)
	}
	created, err := cp.PublishVersion(context.Background(), auth, workerVersionRequest("v1"))
	if err != nil {
		t.Fatal(err)
	}
	updated, err := cp.UpdateWorkerVersionDescription(context.Background(), auth, "payments-worker", "v1", created.Revision, "Corrected release note.")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Description != "Corrected release note." || updated.Revision != created.Revision+1 {
		t.Fatalf("updated version = %#v", updated)
	}
	if !updated.UpdatedAt.After(created.UpdatedAt) {
		t.Fatalf("description patch did not advance updatedAt: before=%s after=%s", created.UpdatedAt, updated.UpdatedAt)
	}
	if updated.Image != created.Image || updated.Runtime.CPU != created.Runtime.CPU || updated.Runtime.Memory != created.Runtime.Memory || updated.Runtime.ServiceAccount != created.Runtime.ServiceAccount || updated.TaskQueue != created.TaskQueue || updated.WorkerDeployment != created.WorkerDeployment || updated.KubernetesDeployment != created.KubernetesDeployment {
		t.Fatalf("description patch changed immutable runtime identity: before=%#v after=%#v", created, updated)
	}
	if _, err := cp.UpdateWorkerVersionDescription(context.Background(), auth, "payments-worker", "v1", created.Revision, "stale update"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale revision = %v", err)
	}
	audits := store.Audits(tenant.ID)
	found := false
	for _, audit := range audits {
		if audit.Action == "worker.version.description.update" && audit.Outcome == "success" {
			found = audit.References["description"] == "" && audit.References["oldDescriptionDigest"] != "" && audit.References["descriptionDigest"] != ""
		}
	}
	if !found {
		t.Fatalf("description audit leaked text or missed digest: %#v", audits)
	}
}

func testTenant(id, slug string) domain.Tenant {
	now := time.Now().UTC()
	return domain.Tenant{
		ID: id, Slug: slug, DisplayName: strings.ToUpper(slug), Status: domain.TenantActive,
		QuotaPolicy: domain.TenantQuotaPolicy{MaxReservedCPU: "4", MaxReservedMemory: "4Gi", MaxActiveWorkerPods: 8, MaxActiveReleases: 8, MaxConcurrentRuns: 8, MaxConcurrentDeployments: 2},
		Revision:    1, CreatedAt: now, UpdatedAt: now,
	}
}

func authFor(tenant domain.Tenant) AuthenticatedContext {
	return AuthenticatedContext{
		PrincipalID: "principal-" + tenant.ID, TenantID: tenant.ID, TenantSlug: tenant.Slug,
		AuthenticationMethod: "test", RequestID: "request-1",
		Permissions: map[string]bool{
			PermissionWorkerCreate: true, PermissionWorkerRead: true, PermissionWorkerDeploy: true, PermissionWorkerVersionUpdate: true, PermissionRunStart: true, PermissionRunRead: true,
			PermissionRunSignal: true, PermissionRunQuery: true, PermissionRunCancel: true,
		},
	}
}

func newTestControlPlane(t *testing.T, cfg Config, cluster Cluster, executor Executor) (*ControlPlane, AuthenticatedContext) {
	t.Helper()
	store := NewMemoryStore()
	tenant := testTenant("tenant-test", "test-tenant")
	if err := store.SaveTenant(tenant); err != nil {
		t.Fatal(err)
	}
	cp := New(cfg, store, cluster, executor)
	auth := authFor(tenant)
	if _, err := cp.CreateWorker(context.Background(), auth, CreateWorkerRequest{WorkerName: "payments-worker"}); err != nil {
		t.Fatal(err)
	}
	return cp, auth
}
