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

func TestFileStoreTenantCreationPersistenceFailureLeavesNoTenantMemberOrAudit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	tenant := domain.Tenant{ID: "tenant-failed", Slug: "failed-create", DisplayName: "Failed Create", Status: domain.TenantActive, QuotaPolicy: domain.DefaultTenantQuotaPolicy(), Revision: 1, CreatedAt: now, UpdatedAt: now}
	owner := domain.TenantMember{TenantID: tenant.ID, PrincipalID: "owner", PrincipalDisplayName: "Owner", Role: domain.TenantRoleOwner, Revision: 1, CreatedAt: now, UpdatedAt: now}
	injected := errors.New("injected Tenant creation persistence failure")
	store.persistSnapshot = func(fileState) error { return injected }
	if err := store.CommitTenantCreation(tenant, owner, domain.AuditRecord{ID: "audit-create", TenantID: tenant.ID}); !errors.Is(err, injected) {
		t.Fatalf("creation error=%v", err)
	}
	if _, ok := store.Tenant(tenant.ID); ok || len(store.TenantMembers(tenant.ID)) != 0 || len(store.Audits(tenant.ID)) != 0 {
		t.Fatalf("failed creation changed live state: tenant=%v members=%#v audits=%#v", ok, store.TenantMembers(tenant.ID), store.Audits(tenant.ID))
	}
	reopened, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reopened.Tenant(tenant.ID); ok || len(reopened.TenantMembers(tenant.ID)) != 0 || len(reopened.Audits(tenant.ID)) != 0 {
		t.Fatalf("failed creation changed disk state: tenant=%v members=%#v audits=%#v", ok, reopened.TenantMembers(tenant.ID), reopened.Audits(tenant.ID))
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

func TestFileStoreInvocationReservationAndTerminalLeaseTransitionAreAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	tenant := testTenant("tenant-invocation", "invocation")
	if err := store.SaveTenant(tenant); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	invocation := domain.Invocation{ID: "inv-1", TenantID: tenant.ID, TenantSlug: tenant.Slug, State: domain.InvocationStarting, TemporalWorkflowID: "workflow-1", TaskQueue: "queue-1", WorkerDeployment: "deployment-1", SelectedVersion: "v1", CreatedAt: now, UpdatedAt: now}
	lease := domain.QuotaLease{ID: "run:" + invocation.ID, TenantID: tenant.ID, Kind: domain.QuotaLeaseRun, ConcurrentRuns: 1, CreatedAt: now}
	injected := errors.New("injected invocation persist failure")
	store.persistSnapshot = func(fileState) error { return injected }
	if err := store.CommitInvocationReservation(tenant.ID, invocation, lease); !errors.Is(err, injected) {
		t.Fatalf("CommitInvocationReservation error = %v", err)
	}
	if _, ok := store.Invocation(tenant.ID, invocation.ID); ok || len(store.QuotaLeases(tenant.ID)) != 0 {
		t.Fatalf("failed reservation changed live state: invocation=%v leases=%#v", ok, store.QuotaLeases(tenant.ID))
	}

	store.persistSnapshot = store.writeSnapshot
	if err := store.CommitInvocationReservation(tenant.ID, invocation, lease); err != nil {
		t.Fatal(err)
	}
	running := invocation
	running.State, running.TemporalRunID = domain.InvocationRunning, "run-1"
	if err := store.SaveInvocation(tenant.ID, running); err != nil {
		t.Fatal(err)
	}
	terminal := running
	terminal.State = domain.InvocationCompleted
	store.persistSnapshot = func(fileState) error { return injected }
	if err := store.CommitInvocationTerminal(tenant.ID, terminal, lease.ID); !errors.Is(err, injected) {
		t.Fatalf("CommitInvocationTerminal error = %v", err)
	}
	if got, _ := store.Invocation(tenant.ID, invocation.ID); got.State != domain.InvocationRunning || len(store.QuotaLeases(tenant.ID)) != 1 {
		t.Fatalf("failed terminal commit changed live state: invocation=%#v leases=%#v", got, store.QuotaLeases(tenant.ID))
	}

	reopened, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := reopened.Invocation(tenant.ID, invocation.ID); got.State != domain.InvocationRunning || got.TemporalWorkflowID != invocation.TemporalWorkflowID || got.TemporalRunID != running.TemporalRunID || got.TaskQueue != invocation.TaskQueue || got.WorkerDeployment != invocation.WorkerDeployment || len(reopened.QuotaLeases(tenant.ID)) != 1 {
		t.Fatalf("failed terminal commit changed disk state: invocation=%#v leases=%#v", got, reopened.QuotaLeases(tenant.ID))
	}
}

func TestFileStoreCurrentWorkerVersionTransitionIsAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	tenant := testTenant("tenant-current", "current")
	if err := store.SaveTenant(tenant); err != nil {
		t.Fatal(err)
	}
	worker := domain.Worker{TenantID: tenant.ID, TenantSlug: tenant.Slug, Name: "worker", CurrentVersion: "v1"}
	if err := store.SaveWorker(tenant.ID, worker); err != nil {
		t.Fatal(err)
	}
	v1 := domain.WorkerVersion{ID: "ver-1", TenantID: tenant.ID, TenantSlug: tenant.Slug, WorkerName: worker.Name, Version: "v1", State: domain.WorkerVersionReady, Current: true}
	v2 := domain.WorkerVersion{ID: "ver-2", TenantID: tenant.ID, TenantSlug: tenant.Slug, WorkerName: worker.Name, Version: "v2", State: domain.WorkerVersionPending, TaskQueue: "queue-2", WorkerDeployment: "deployment-2", KubernetesDeployment: "kube-2"}
	if err := store.SaveWorkerVersion(tenant.ID, v1); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveWorkerVersion(tenant.ID, v2); err != nil {
		t.Fatal(err)
	}
	worker.CurrentVersion = "v2"
	v2.State, v2.Current = domain.WorkerVersionReady, true
	injected := errors.New("injected current transition failure")
	store.persistSnapshot = func(fileState) error { return injected }
	if err := store.CommitCurrentWorkerVersion(tenant.ID, worker, v2, nil); !errors.Is(err, injected) {
		t.Fatalf("CommitCurrentWorkerVersion error = %v", err)
	}
	gotWorker, _ := store.Worker(tenant.ID, worker.Name)
	gotV1, _ := store.WorkerVersion(tenant.ID, worker.Name, "v1")
	gotV2, _ := store.WorkerVersion(tenant.ID, worker.Name, "v2")
	if gotWorker.CurrentVersion != "v1" || !gotV1.Current || gotV2.Current || gotV2.State != domain.WorkerVersionPending {
		t.Fatalf("failed transition changed live state: worker=%#v v1=%#v v2=%#v", gotWorker, gotV1, gotV2)
	}
	reopened, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	gotWorker, _ = reopened.Worker(tenant.ID, worker.Name)
	gotV1, _ = reopened.WorkerVersion(tenant.ID, worker.Name, "v1")
	gotV2, _ = reopened.WorkerVersion(tenant.ID, worker.Name, "v2")
	if gotWorker.CurrentVersion != "v1" || !gotV1.Current || gotV2.Current || gotV2.State != domain.WorkerVersionPending || gotV2.TaskQueue != v2.TaskQueue || gotV2.WorkerDeployment != v2.WorkerDeployment || gotV2.KubernetesDeployment != v2.KubernetesDeployment {
		t.Fatalf("failed transition changed disk state: worker=%#v v1=%#v v2=%#v", gotWorker, gotV1, gotV2)
	}
}
