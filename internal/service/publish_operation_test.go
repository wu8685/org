package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wu8685/org/internal/domain"
)

func TestPublishOperationReservationIsPrincipalScopedIdempotentAndAudited(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	cp, auth := newTestControlPlane(t, Config{Now: func() time.Time { return now }, PublishOperationRetention: 24 * time.Hour}, &fakeCluster{}, &fakeExecutor{})
	request := PublishOperationReservation{IdempotencyKey: "publish-42", PayloadDigest: "sha256:payload-a", WorkerName: "payments-worker", Version: "v1"}

	first, created, err := cp.ReservePublishOperation(context.Background(), auth, request)
	if err != nil || !created || first.ID == "" || first.IdempotencyKeyHash == "" || first.IdempotencyKeyHash == request.IdempotencyKey {
		t.Fatalf("first=%#v created=%v err=%v", first, created, err)
	}
	replayed, created, err := cp.ReservePublishOperation(context.Background(), auth, request)
	if err != nil || created || replayed.ID != first.ID {
		t.Fatalf("replayed=%#v created=%v err=%v", replayed, created, err)
	}

	conflict := request
	conflict.PayloadDigest = "sha256:payload-b"
	if _, _, err := cp.ReservePublishOperation(context.Background(), auth, conflict); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict error = %v", err)
	}

	otherPrincipal := auth
	otherPrincipal.PrincipalID = "another-user"
	separate, created, err := cp.ReservePublishOperation(context.Background(), otherPrincipal, request)
	if err != nil || !created || separate.ID == first.ID {
		t.Fatalf("principal-scoped reservation=%#v created=%v err=%v", separate, created, err)
	}
	otherTenant := testTenant("tenant-other", "other")
	if err := cp.store.SaveTenant(otherTenant); err != nil {
		t.Fatal(err)
	}
	otherTenantAuth := authFor(otherTenant)
	otherTenantAuth.PrincipalID = auth.PrincipalID
	tenantScoped, created, err := cp.ReservePublishOperation(context.Background(), otherTenantAuth, request)
	if err != nil || !created || tenantScoped.ID == first.ID {
		t.Fatalf("tenant-scoped reservation=%#v created=%v err=%v", tenantScoped, created, err)
	}

	audits := cp.store.Audits(auth.TenantID)
	if len(audits) < 4 {
		t.Fatalf("audits=%#v", audits)
	}
	for _, audit := range audits {
		if strings.Contains(audit.References["idempotencyKeyHash"], request.IdempotencyKey) || strings.Contains(audit.References["idempotencyKey"], request.IdempotencyKey) {
			t.Fatalf("audit leaked raw idempotency key: %#v", audit)
		}
	}
}

func TestCompletedPublishOperationExpiresButRunningReservationDoesNot(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	cp, auth := newTestControlPlane(t, Config{Now: func() time.Time { return now }, PublishOperationRetention: time.Hour}, &fakeCluster{}, &fakeExecutor{})
	request := PublishOperationReservation{IdempotencyKey: "publish-expiry", PayloadDigest: "sha256:payload", WorkerName: "payments-worker", Version: "v1"}

	running, created, err := cp.ReservePublishOperation(context.Background(), auth, request)
	if err != nil || !created {
		t.Fatalf("reserve=%#v created=%v err=%v", running, created, err)
	}
	now = now.Add(2 * time.Hour)
	stillRunning, created, err := cp.ReservePublishOperation(context.Background(), auth, request)
	if err != nil || created || stillRunning.ID != running.ID {
		t.Fatalf("running reservation expired: %#v created=%v err=%v", stillRunning, created, err)
	}

	completed, err := cp.CompletePublishOperation(context.Background(), auth, running.ID, domain.WorkerVersion{ID: "ver-1", Version: "v1"}, "", "")
	if err != nil || completed.State != domain.PublishOperationSucceeded {
		t.Fatalf("complete=%#v err=%v", completed, err)
	}
	now = now.Add(2 * time.Hour)
	replacement, created, err := cp.ReservePublishOperation(context.Background(), auth, request)
	if err != nil || !created || replacement.ID == running.ID {
		t.Fatalf("expired replacement=%#v created=%v err=%v", replacement, created, err)
	}
}
