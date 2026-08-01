package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wu8685/org/internal/domain"
	"github.com/wu8685/org/sdk/orgsdk"
)

func TestBootstrapRegistrationFreezesContractAndReturnsExactRetryReceipt(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	version := bootstrapPendingVersion(t)
	if err := store.SaveWorkerVersion(version.TenantID, version); err != nil {
		t.Fatal(err)
	}
	registry := NewBootstrapRegistry(store, BootstrapRegistryConfig{
		TTL: 15 * time.Minute, ReceiptGrace: 5 * time.Minute, Now: func() time.Time { return now },
		Verifier: BootstrapWorkloadVerifierFunc(func(_ context.Context, binding domain.BootstrapBinding, evidence BootstrapWorkloadEvidence) error {
			if evidence.ObservedImage != binding.ExpectedImage || !evidence.AudienceVerified {
				return errors.New("workload identity mismatch")
			}
			return nil
		}),
	})
	material, err := registry.Issue(version, "generation-1")
	if err != nil {
		t.Fatal(err)
	}
	if material.Token == "" || strings.Contains(mustJSON(t, store.BootstrapCredentials()), material.Token) {
		t.Fatal("raw bootstrap token was missing or persisted")
	}
	registration := bootstrapContract(t, version.Version)
	evidence := BootstrapWorkloadEvidence{ObservedImage: version.Image, PodUID: "pod-1", AudienceVerified: true}
	first, err := registry.Register(context.Background(), material.Token, evidence, registration)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Register(context.Background(), material.Token, BootstrapWorkloadEvidence{ObservedImage: "forged", AudienceVerified: true}, registration); !errors.Is(err, ErrBootstrapRejected) {
		t.Fatalf("forged retry error = %v", err)
	}
	second, err := registry.Register(context.Background(), material.Token, evidence, registration)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || !second.ExactRetry {
		t.Fatalf("receipts first=%#v second=%#v", first, second)
	}
	stored, ok := store.WorkerVersion(version.TenantID, version.WorkerName, version.Version)
	if !ok || stored.ManifestDigest != registration.ManifestDigest || stored.RegistrationStatus != domain.BootstrapRegistrationAccepted {
		t.Fatalf("stored version = %#v", stored)
	}
	audits := store.Audits(version.TenantID)
	if len(audits) == 0 || audits[len(audits)-1].Action != "worker.contract.register" || strings.Contains(mustJSON(t, audits), material.Token) {
		t.Fatalf("bootstrap audits = %#v", audits)
	}
	registration.BuildID = "v2"
	if _, err := registry.Register(context.Background(), material.Token, evidence, registration); !errors.Is(err, ErrBootstrapConflict) {
		t.Fatalf("different retry error = %v", err)
	}
}

func TestBootstrapRegistrationRejectsExpiredOrWrongImageWithoutWritingContract(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	version := bootstrapPendingVersion(t)
	if err := store.SaveWorkerVersion(version.TenantID, version); err != nil {
		t.Fatal(err)
	}
	registry := NewBootstrapRegistry(store, BootstrapRegistryConfig{
		TTL: time.Minute, Now: func() time.Time { return now },
		Verifier: BootstrapWorkloadVerifierFunc(func(_ context.Context, binding domain.BootstrapBinding, evidence BootstrapWorkloadEvidence) error {
			if evidence.ObservedImage != binding.ExpectedImage {
				return errors.New("image mismatch")
			}
			return nil
		}),
	})
	material, err := registry.Issue(version, "generation-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Register(context.Background(), material.Token, BootstrapWorkloadEvidence{ObservedImage: "registry.example.com/acme/other@sha256:" + strings.Repeat("b", 64)}, bootstrapContract(t, version.Version)); !errors.Is(err, ErrBootstrapRejected) {
		t.Fatalf("wrong image error = %v", err)
	}
	stored, _ := store.WorkerVersion(version.TenantID, version.WorkerName, version.Version)
	stored.RegistrationStatus = domain.BootstrapRegistrationAwaiting
	if err := store.SaveWorkerVersion(stored.TenantID, stored); err != nil {
		t.Fatal(err)
	}
	material, err = registry.Issue(stored, "generation-2")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := registry.Register(context.Background(), material.Token, BootstrapWorkloadEvidence{ObservedImage: version.Image}, bootstrapContract(t, version.Version)); !errors.Is(err, ErrBootstrapExpired) {
		t.Fatalf("expired error = %v", err)
	}
	stored, _ = store.WorkerVersion(version.TenantID, version.WorkerName, version.Version)
	if stored.ManifestDigest != "" || len(stored.Metadata.Workflows) != 0 {
		t.Fatalf("rejected contract persisted: %#v", stored)
	}
}

func TestStrictBootstrapVerifierRequiresAudienceAndExactImageIdentity(t *testing.T) {
	verifier := StrictBootstrapWorkloadVerifier{}
	binding := domain.BootstrapBinding{ExpectedImage: "registry.example.com/acme/worker@sha256:" + strings.Repeat("a", 64), ExpectedServiceAccount: "org-worker"}
	if err := verifier.VerifyBootstrapWorkload(context.Background(), binding, BootstrapWorkloadEvidence{AudienceVerified: true, PodUID: "pod-1", ServiceAccount: "org-worker", ObservedImage: binding.ExpectedImage}); err != nil {
		t.Fatal(err)
	}
	for _, evidence := range []BootstrapWorkloadEvidence{
		{PodUID: "pod-1", ServiceAccount: "org-worker", ObservedImage: binding.ExpectedImage},
		{AudienceVerified: true, PodUID: "pod-1", ServiceAccount: "org-worker", ObservedImage: "registry.example.com/acme/worker@sha256:" + strings.Repeat("b", 64)},
		{AudienceVerified: true, ServiceAccount: "org-worker", ObservedImage: binding.ExpectedImage},
		{AudienceVerified: true, PodUID: "pod-1", ServiceAccount: "other", ObservedImage: binding.ExpectedImage},
	} {
		if err := verifier.VerifyBootstrapWorkload(context.Background(), binding, evidence); err == nil {
			t.Fatalf("unsafe evidence accepted: %#v", evidence)
		}
	}
}

func bootstrapPendingVersion(t *testing.T) domain.WorkerVersion {
	t.Helper()
	return domain.WorkerVersion{ID: "ver-1", TenantID: "tenant-1", TenantSlug: "acme", WorkerName: "payments-worker", Version: "v1", Image: "registry.example.com/acme/payments@sha256:" + strings.Repeat("a", 64), State: domain.WorkerVersionPending}
}

func bootstrapContract(t *testing.T, buildID string) domain.WorkerContractRegistration {
	t.Helper()
	definition := dynamicServiceDefinition()
	manifest, digest, err := orgsdk.GenerateManifest("DynamicWorkflow", definition)
	if err != nil {
		t.Fatal(err)
	}
	var metadata domain.WorkerMetadata
	if err := json.Unmarshal(manifest, &metadata); err != nil {
		t.Fatal(err)
	}
	return domain.WorkerContractRegistration{ManifestDigest: digest, Metadata: metadata, BuildID: buildID}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
