package service

import (
	"path/filepath"
	"testing"

	"github.com/wu8685/org/internal/domain"
)

func TestFileStorePersistsDeploymentAndInvocationAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	tenant := testTenant("tenant-file", "file-tenant")
	if err := store.SaveTenant(tenant); err != nil {
		t.Fatal(err)
	}
	worker := domain.Worker{TenantID: tenant.ID, TenantSlug: tenant.Slug, Name: "payments-worker", CurrentVersion: "v1"}
	if err := store.SaveWorker(tenant.ID, worker); err != nil {
		t.Fatal(err)
	}
	d := domain.WorkerVersion{ID: "ver-1", TenantID: tenant.ID, TenantSlug: tenant.Slug, WorkerName: "payments-worker", Version: "v1", Description: "Initial release.", Revision: 1, Current: true}
	i := domain.Invocation{ID: "inv-1", TenantID: tenant.ID, TenantSlug: tenant.Slug, WorkerName: "payments-worker", Workflow: "ChargeOrder", IdempotencyKey: "checkout-42"}
	if err := store.SaveWorkerVersion(tenant.ID, d); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveInvocation(tenant.ID, i); err != nil {
		t.Fatal(err)
	}
	operation := domain.ActionOperation{ID: "act-1", TenantID: tenant.ID, RunID: i.ID, RuntimeNodeID: "approval", Action: "confirm", OperationID: "op-1", PayloadDigest: "digest", State: domain.ActionDeliveryDelivered}
	if err := store.SaveActionOperation(tenant.ID, operation); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateWorkerVersionDescription(tenant.ID, worker.Name, "v1", 1, "Corrected release."); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if gotTenant, ok := reopened.Tenant(tenant.ID); !ok || gotTenant.Slug != tenant.Slug {
		t.Fatalf("tenant = %#v, %v", gotTenant, ok)
	}
	if gotWorker, ok := reopened.Worker(tenant.ID, worker.Name); !ok || gotWorker.CurrentVersion != "v1" {
		t.Fatalf("worker = %#v, %v", gotWorker, ok)
	}
	if got := reopened.WorkerVersions(tenant.ID, "payments-worker"); len(got) != 1 || got[0].ID != d.ID || got[0].Description != "Corrected release." || got[0].Revision != 2 {
		t.Fatalf("deployments = %#v", got)
	}
	if got, ok := reopened.Invocation(tenant.ID, "inv-1"); !ok || got.IdempotencyKey != i.IdempotencyKey {
		t.Fatalf("invocation = %#v, %v", got, ok)
	}
	if got, ok := reopened.ActionOperation(tenant.ID, i.ID, operation.RuntimeNodeID, operation.Action, operation.OperationID); !ok || got.State != domain.ActionDeliveryDelivered {
		t.Fatalf("action operation = %#v, %v", got, ok)
	}
}
