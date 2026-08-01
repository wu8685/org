package service

import (
	"context"
	"testing"
	"time"

	"github.com/wu8685/org/internal/domain"
)

func TestCatalogReadsStayTenantScopedAndDeterministicallySorted(t *testing.T) {
	store := NewMemoryStore()
	tenantA := testTenant("tenant-a", "alpha")
	tenantB := testTenant("tenant-b", "beta")
	for _, tenant := range []domain.Tenant{tenantA, tenantB} {
		if err := store.SaveTenant(tenant); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	for _, worker := range []domain.Worker{
		{TenantID: tenantA.ID, TenantSlug: tenantA.Slug, Name: "zeta-worker", CreatedAt: now},
		{TenantID: tenantA.ID, TenantSlug: tenantA.Slug, Name: "alpha-worker", CurrentVersion: "v2", CreatedAt: now},
		{TenantID: tenantB.ID, TenantSlug: tenantB.Slug, Name: "hidden-worker", CreatedAt: now},
	} {
		if err := store.SaveWorker(worker.TenantID, worker); err != nil {
			t.Fatal(err)
		}
	}
	versions := []domain.WorkerVersion{
		catalogVersion(tenantA, "alpha-worker", "v1", false, now.Add(-time.Hour)),
		catalogVersion(tenantA, "alpha-worker", "v2", true, now),
		catalogVersion(tenantB, "hidden-worker", "v1", true, now),
	}
	for _, version := range versions {
		if err := store.SaveWorkerVersion(version.TenantID, version); err != nil {
			t.Fatal(err)
		}
	}
	invocations := []domain.Invocation{
		{ID: "run-old", TenantID: tenantA.ID, TenantSlug: tenantA.Slug, WorkerName: "alpha-worker", Workflow: "HelloWorkflow", SelectedVersion: "v1", CreatedAt: now.Add(-time.Hour)},
		{ID: "run-new", TenantID: tenantA.ID, TenantSlug: tenantA.Slug, WorkerName: "alpha-worker", Workflow: "HelloWorkflow", SelectedVersion: "v2", CreatedAt: now},
		{ID: "run-hidden", TenantID: tenantB.ID, TenantSlug: tenantB.Slug, WorkerName: "hidden-worker", Workflow: "HelloWorkflow", SelectedVersion: "v1", CreatedAt: now},
	}
	for _, invocation := range invocations {
		if err := store.SaveInvocation(invocation.TenantID, invocation); err != nil {
			t.Fatal(err)
		}
	}
	operation := domain.ActionOperation{ID: "action-a", TenantID: tenantA.ID, RunID: "run-new", RuntimeNodeID: "approval", Action: "confirm", OperationID: "op-1", State: domain.ActionDeliveryDelivered}
	if err := store.SaveActionOperation(tenantA.ID, operation); err != nil {
		t.Fatal(err)
	}

	cp := New(Config{}, store, &fakeCluster{}, &fakeExecutor{})
	auth := authFor(tenantA)
	workers, err := cp.ListWorkers(context.Background(), auth)
	if err != nil {
		t.Fatal(err)
	}
	if len(workers) != 2 || workers[0].Name != "alpha-worker" || workers[1].Name != "zeta-worker" {
		t.Fatalf("workers = %#v", workers)
	}
	gotVersions, err := cp.ListWorkerVersions(context.Background(), auth, "alpha-worker")
	if err != nil {
		t.Fatal(err)
	}
	if len(gotVersions) != 2 || gotVersions[0].Version != "v2" || !gotVersions[0].Current || gotVersions[1].Version != "v1" {
		t.Fatalf("versions = %#v", gotVersions)
	}
	if _, err := cp.GetWorkerVersion(context.Background(), auth, "hidden-worker", "v1"); err != ErrNotFound {
		t.Fatalf("cross-Tenant version read = %v", err)
	}
	runs, err := cp.ListInvocations(context.Background(), auth, InvocationFilter{WorkerName: "alpha-worker", Workflow: "HelloWorkflow"})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || runs[0].ID != "run-new" || runs[1].ID != "run-old" {
		t.Fatalf("runs = %#v", runs)
	}
	actions, err := cp.ListRunActions(context.Background(), auth, "run-new")
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 1 || actions[0].OperationID != "op-1" {
		t.Fatalf("actions = %#v", actions)
	}
}

func TestWorkflowCatalogContainsOnlyReadyTenantVersions(t *testing.T) {
	store := NewMemoryStore()
	tenant := testTenant("tenant-a", "alpha")
	if err := store.SaveTenant(tenant); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveWorker(tenant.ID, domain.Worker{TenantID: tenant.ID, TenantSlug: tenant.Slug, Name: "hello-worker", CurrentVersion: "v2"}); err != nil {
		t.Fatal(err)
	}
	ready := catalogVersion(tenant, "hello-worker", "v2", true, time.Now().UTC())
	failed := catalogVersion(tenant, "hello-worker", "v1", false, time.Now().Add(-time.Hour).UTC())
	failed.State = domain.WorkerVersionFailed
	for _, version := range []domain.WorkerVersion{ready, failed} {
		if err := store.SaveWorkerVersion(tenant.ID, version); err != nil {
			t.Fatal(err)
		}
	}
	cp := New(Config{}, store, &fakeCluster{}, &fakeExecutor{})
	items, err := cp.ListWorkflowCatalog(context.Background(), authFor(tenant))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].WorkerName != "hello-worker" || items[0].WorkerVersion != "v2" || !items[0].Current || items[0].Workflow.Name != "HelloWorkflow" {
		t.Fatalf("catalog = %#v", items)
	}
}

func TestOverviewReportsOnlyAuthenticatedTenantQuotaAndRecordCounts(t *testing.T) {
	store := NewMemoryStore()
	tenantA := testTenant("tenant-a", "alpha")
	tenantB := testTenant("tenant-b", "beta")
	for _, tenant := range []domain.Tenant{tenantA, tenantB} {
		if err := store.SaveTenant(tenant); err != nil {
			t.Fatal(err)
		}
		if err := store.SaveWorker(tenant.ID, domain.Worker{TenantID: tenant.ID, TenantSlug: tenant.Slug, Name: tenant.Slug + "-worker"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SaveWorkerVersion(tenantA.ID, catalogVersion(tenantA, "alpha-worker", "v1", true, time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveInvocation(tenantA.ID, domain.Invocation{ID: "run-a", TenantID: tenantA.ID, WorkerName: "alpha-worker"}); err != nil {
		t.Fatal(err)
	}
	if err := store.AcquireQuotaLease(tenantA.ID, domain.QuotaLease{ID: "run:run-a", TenantID: tenantA.ID, Kind: domain.QuotaLeaseRun, ConcurrentRuns: 1}); err != nil {
		t.Fatal(err)
	}

	cp := New(Config{}, store, &fakeCluster{}, &fakeExecutor{})
	overview, err := cp.GetOverview(context.Background(), authFor(tenantA))
	if err != nil {
		t.Fatal(err)
	}
	if overview.TenantID != tenantA.ID || overview.Workers.Total != 1 || overview.Versions.Ready != 1 || overview.Runs.Total != 1 || overview.QuotaUsage.ConcurrentRuns != 1 || overview.QuotaPolicy.MaxConcurrentRuns != tenantA.QuotaPolicy.MaxConcurrentRuns {
		t.Fatalf("overview=%#v", overview)
	}
}

func catalogVersion(tenant domain.Tenant, workerName, version string, current bool, created time.Time) domain.WorkerVersion {
	return domain.WorkerVersion{
		ID: "id-" + workerName + "-" + version, TenantID: tenant.ID, TenantSlug: tenant.Slug,
		WorkerName: workerName, Version: version, Description: "Release " + version, Revision: 1,
		State: domain.WorkerVersionReady, Current: current, CreatedAt: created,
		Metadata: domain.WorkerMetadata{Workflows: []domain.WorkflowContract{{Name: "HelloWorkflow", VersioningBehavior: "pinned", ProjectionQuery: "projection"}}},
	}
}
