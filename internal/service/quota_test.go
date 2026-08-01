package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wu8685/org/internal/domain"
)

func TestQuotaLeaseAdmissionIsAtomicAndReconciliationRemovesStaleLease(t *testing.T) {
	store := NewMemoryStore()
	tenant := testTenant("tenant-quota", "quota")
	tenant.QuotaPolicy.MaxConcurrentRuns = 1
	if err := store.SaveTenant(tenant); err != nil {
		t.Fatal(err)
	}

	var admitted atomic.Int32
	var wait sync.WaitGroup
	for n := 0; n < 16; n++ {
		wait.Add(1)
		go func(n int) {
			defer wait.Done()
			lease := domain.QuotaLease{ID: newID("lease"), TenantID: tenant.ID, Kind: domain.QuotaLeaseRun, ConcurrentRuns: 1, CreatedAt: time.Now().UTC()}
			if err := store.AcquireQuotaLease(tenant.ID, lease); err == nil {
				admitted.Add(1)
			} else if !errors.Is(err, ErrTenantQuotaExceeded) {
				t.Errorf("unexpected admission error: %v", err)
			}
		}(n)
	}
	wait.Wait()
	if admitted.Load() != 1 {
		t.Fatalf("admitted %d leases, want exactly one", admitted.Load())
	}
	removed, err := store.ReconcileQuotaLeases(tenant.ID, map[string]bool{})
	if err != nil || removed != 1 {
		t.Fatalf("reconcile removed=%d err=%v", removed, err)
	}
	if err := store.AcquireQuotaLease(tenant.ID, domain.QuotaLease{ID: "replacement", TenantID: tenant.ID, Kind: domain.QuotaLeaseRun, ConcurrentRuns: 1, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("stale lease capacity was not restored: %v", err)
	}
}

func TestDeployAndRunQuotaRejectBeforeAdaptersAndCancelReleasesRunLease(t *testing.T) {
	store := NewMemoryStore()
	tenant := testTenant("tenant-limited", "limited")
	tenant.QuotaPolicy.MaxActiveReleases = 1
	tenant.QuotaPolicy.MaxConcurrentRuns = 1
	if err := store.SaveTenant(tenant); err != nil {
		t.Fatal(err)
	}
	cluster := &fakeCluster{}
	executor := &fakeExecutor{}
	cp := New(Config{RegistryAllowlist: []string{"registry.example.com"}}, store, cluster, executor)
	auth := authFor(tenant)
	if _, err := cp.CreateWorker(context.Background(), auth, CreateWorkerRequest{WorkerName: "payments-worker"}); err != nil {
		t.Fatal(err)
	}

	if _, err := cp.PublishVersion(context.Background(), auth, workerVersionRequest("v1")); err != nil {
		t.Fatal(err)
	}
	adapterCalls := len(cluster.calls)
	if _, err := cp.PublishVersion(context.Background(), auth, workerVersionRequest("v2")); !errors.Is(err, ErrTenantQuotaExceeded) {
		t.Fatalf("expected release quota rejection, got %v", err)
	}
	if len(cluster.calls) != adapterCalls {
		t.Fatalf("quota rejection called Kubernetes: before=%d after=%d", adapterCalls, len(cluster.calls))
	}

	first, err := cp.Start(context.Background(), auth, StartRequest{WorkerName: "payments-worker", Workflow: "ChargeOrder", Input: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cp.Start(context.Background(), auth, StartRequest{WorkerName: "payments-worker", Workflow: "ChargeOrder", Input: []byte(`{}`)}); !errors.Is(err, ErrTenantQuotaExceeded) {
		t.Fatalf("expected run quota rejection, got %v", err)
	}
	if len(executor.starts) != 1 {
		t.Fatalf("quota rejection called Temporal: starts=%d", len(executor.starts))
	}
	if err := cp.Cancel(context.Background(), auth, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := cp.Start(context.Background(), auth, StartRequest{WorkerName: "payments-worker", Workflow: "ChargeOrder", Input: []byte(`{}`)}); err != nil {
		t.Fatalf("cancel did not release run lease: %v", err)
	}

	audits := store.Audits(tenant.ID)
	foundQuotaRejection := false
	for _, audit := range audits {
		if audit.ErrorClass == "tenant_quota_exceeded" {
			foundQuotaRejection = true
		}
	}
	if !foundQuotaRejection {
		t.Fatalf("quota rejection missing from audit: %#v", audits)
	}
}
