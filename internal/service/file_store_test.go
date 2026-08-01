package service

import (
	"path/filepath"
	"testing"
	"time"

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
	failureTime := time.Date(2026, 8, 2, 7, 0, 0, 0, time.UTC)
	i := domain.Invocation{ID: "inv-1", TenantID: tenant.ID, TenantSlug: tenant.Slug, WorkerName: "payments-worker", Workflow: "ChargeOrder", Description: "Why this Run exists", IdempotencyKey: "checkout-42", IdempotencyPayloadDigest: "sha256:intent", SafeFailure: &domain.RunFailure{Code: "invalid_route", Message: "Unsupported mode.", RuntimeNodeID: "route-aaaaaaaaaaaaaaaa", TemplateID: "route", NodeLabel: "Determine route", OccurredAt: failureTime}}
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
	publishOperation := domain.PublishOperation{
		ID: "pub-1", TenantID: tenant.ID, PrincipalID: "principal-1", IdempotencyKeyHash: "key-hash", PayloadDigest: "payload-hash",
		WorkerName: worker.Name, Version: "v2", State: domain.PublishOperationRunning, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if _, created, err := store.ReservePublishOperation(publishOperation, time.Now().UTC()); err != nil || !created {
		t.Fatalf("reserve publish operation: created=%v err=%v", created, err)
	}
	credential := domain.BootstrapCredential{TokenHash: "sha256-hash-only", Binding: domain.BootstrapBinding{TenantID: tenant.ID, WorkerVersionID: d.ID}}
	if err := store.SaveBootstrapCredential(credential); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateWorkerVersionDescription(tenant.ID, worker.Name, "v1", 1, "Corrected release."); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTenantSelection("local-session", tenant.ID); err != nil {
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
	if got, ok := reopened.Invocation(tenant.ID, "inv-1"); !ok || got.IdempotencyKey != i.IdempotencyKey || got.Description != i.Description || got.IdempotencyPayloadDigest != i.IdempotencyPayloadDigest || got.SafeFailure == nil || got.SafeFailure.Code != "invalid_route" || !got.SafeFailure.OccurredAt.Equal(failureTime) {
		t.Fatalf("invocation = %#v, %v", got, ok)
	}
	if got, ok := reopened.ActionOperation(tenant.ID, i.ID, operation.RuntimeNodeID, operation.Action, operation.OperationID); !ok || got.State != domain.ActionDeliveryDelivered {
		t.Fatalf("action operation = %#v, %v", got, ok)
	}
	if got, ok := reopened.PublishOperation(tenant.ID, publishOperation.ID); !ok || got.PayloadDigest != publishOperation.PayloadDigest {
		t.Fatalf("publish operation = %#v, %v", got, ok)
	}
	if replayed, created, err := reopened.ReservePublishOperation(publishOperation, time.Now().UTC()); err != nil || created || replayed.ID != publishOperation.ID {
		t.Fatalf("replayed publish operation = %#v, created=%v err=%v", replayed, created, err)
	}
	if got, ok := reopened.BootstrapCredential(credential.TokenHash); !ok || got.Binding.WorkerVersionID != d.ID {
		t.Fatalf("bootstrap credential = %#v, %v", got, ok)
	}
	if got, ok := reopened.TenantSelection("local-session"); !ok || got != tenant.ID {
		t.Fatalf("Tenant selection = %q, %v", got, ok)
	}
}
