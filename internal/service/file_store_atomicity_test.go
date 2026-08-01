package service

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/wu8685/org/internal/domain"
)

func TestFileStorePersistenceFailureLeavesLiveAndDiskSnapshotsUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	tenant := testTenant("tenant-atomic", "atomic")
	if err := store.SaveTenant(tenant); err != nil {
		t.Fatal(err)
	}
	version := domain.WorkerVersion{ID: "ver-1", TenantID: tenant.ID, TenantSlug: tenant.Slug, WorkerName: "worker", Version: "v1", Description: "before", Revision: 1}
	if err := store.SaveWorkerVersion(tenant.ID, version); err != nil {
		t.Fatal(err)
	}
	lease := domain.QuotaLease{ID: "run:1", TenantID: tenant.ID, Kind: domain.QuotaLeaseRun, ConcurrentRuns: 1, CreatedAt: time.Now().UTC()}
	if err := store.AcquireQuotaLease(tenant.ID, lease); err != nil {
		t.Fatal(err)
	}

	injected := errors.New("injected persist failure")
	store.persistSnapshot = func(fileState) error { return injected }
	worker := domain.Worker{TenantID: tenant.ID, TenantSlug: tenant.Slug, Name: "worker"}
	if err := store.SaveWorker(tenant.ID, worker); !errors.Is(err, injected) {
		t.Fatalf("SaveWorker error = %v", err)
	}
	if _, ok := store.Worker(tenant.ID, worker.Name); ok {
		t.Fatal("failed SaveWorker changed live state")
	}
	if _, err := store.UpdateWorkerVersionDescription(tenant.ID, "worker", "v1", 1, "after"); !errors.Is(err, injected) {
		t.Fatalf("Update description error = %v", err)
	}
	if got, _ := store.WorkerVersion(tenant.ID, "worker", "v1"); got.Description != "before" || got.Revision != 1 {
		t.Fatalf("failed update changed live state: %#v", got)
	}
	if err := store.ReleaseQuotaLease(tenant.ID, lease.ID); !errors.Is(err, injected) {
		t.Fatalf("ReleaseQuotaLease error = %v", err)
	}
	if got := store.QuotaLeases(tenant.ID); len(got) != 1 || got[0].ID != lease.ID {
		t.Fatalf("failed release changed live leases: %#v", got)
	}
	secondLease := domain.QuotaLease{ID: "run:2", TenantID: tenant.ID, Kind: domain.QuotaLeaseRun, ConcurrentRuns: 1}
	if err := store.AcquireQuotaLease(tenant.ID, secondLease); !errors.Is(err, injected) {
		t.Fatalf("AcquireQuotaLease error = %v", err)
	}
	if got := store.QuotaLeases(tenant.ID); len(got) != 1 || got[0].ID != lease.ID {
		t.Fatalf("failed acquire changed live leases: %#v", got)
	}

	reopened, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reopened.Worker(tenant.ID, worker.Name); ok {
		t.Fatal("failed SaveWorker changed disk state")
	}
	if got, _ := reopened.WorkerVersion(tenant.ID, "worker", "v1"); got.Description != "before" || got.Revision != 1 {
		t.Fatalf("failed update changed disk state: %#v", got)
	}
	if got := reopened.QuotaLeases(tenant.ID); len(got) != 1 || got[0].ID != lease.ID {
		t.Fatalf("failed quota mutation changed disk leases: %#v", got)
	}
}

func TestFileStoreFailedQuotaReconcileReportsNoCommittedRemoval(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	tenant := testTenant("tenant-reconcile", "reconcile")
	if err := store.SaveTenant(tenant); err != nil {
		t.Fatal(err)
	}
	lease := domain.QuotaLease{ID: "run:1", TenantID: tenant.ID, Kind: domain.QuotaLeaseRun, ConcurrentRuns: 1}
	if err := store.AcquireQuotaLease(tenant.ID, lease); err != nil {
		t.Fatal(err)
	}
	injected := errors.New("injected persist failure")
	store.persistSnapshot = func(fileState) error { return injected }
	removed, err := store.ReconcileQuotaLeases(tenant.ID, map[string]bool{})
	if !errors.Is(err, injected) || removed != 0 {
		t.Fatalf("removed=%d error=%v", removed, err)
	}
	if got := store.QuotaLeases(tenant.ID); len(got) != 1 || got[0].ID != lease.ID {
		t.Fatalf("failed reconcile changed live leases: %#v", got)
	}
}

func TestFileStorePromotionTransitionAndAuditCommitAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	tenant := testTenant("tenant-promotion", "promotion")
	if err := store.SaveTenant(tenant); err != nil {
		t.Fatal(err)
	}
	version := domain.WorkerVersion{
		ID: "ver-1", TenantID: tenant.ID, TenantSlug: tenant.Slug,
		WorkerName: "worker", Version: "v1", State: domain.WorkerVersionPending,
		PromotionPhase: domain.WorkerVersionPromotionQueued, PromotionAttemptID: "promotion-1",
	}
	if err := store.SaveWorkerVersion(tenant.ID, version); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	candidate := version
	candidate.PromotionPhase, candidate.PromotionUpdatedAt = domain.WorkerVersionPromotionProbing, &now
	audit := domain.AuditRecord{
		ID: "audit-1", TenantID: tenant.ID, TenantSlug: tenant.Slug,
		PrincipalID: "bootstrap-promotion-controller", AuthenticationMethod: "internal-controller",
		RequestID: version.PromotionAttemptID,
		Action:    "worker.version.promotion.probing-contract", Permission: "bootstrap:promote",
		AuthorizationResult: "allowed", Outcome: "in-progress", TargetType: "workerVersion", TargetID: version.ID,
		CreatedAt: now,
	}
	injected := errors.New("injected persist failure")
	store.persistSnapshot = func(fileState) error { return injected }
	if err := store.CommitWorkerVersionAudit(tenant.ID, candidate, audit); !errors.Is(err, injected) {
		t.Fatalf("CommitWorkerVersionAudit error = %v", err)
	}
	if got, _ := store.WorkerVersion(tenant.ID, version.WorkerName, version.Version); got.PromotionPhase != domain.WorkerVersionPromotionQueued {
		t.Fatalf("failed transition changed live version: %#v", got)
	}
	if got := store.Audits(tenant.ID); len(got) != 0 {
		t.Fatalf("failed transition changed live audits: %#v", got)
	}

	reopened, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := reopened.WorkerVersion(tenant.ID, version.WorkerName, version.Version); got.PromotionPhase != domain.WorkerVersionPromotionQueued {
		t.Fatalf("failed transition changed disk version: %#v", got)
	}
	if got := reopened.Audits(tenant.ID); len(got) != 0 {
		t.Fatalf("failed transition changed disk audits: %#v", got)
	}
}
