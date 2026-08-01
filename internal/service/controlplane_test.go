package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wu8685/org/internal/domain"
	"github.com/wu8685/org/sdk/orgsdk"
)

func TestDeployWaitsForKubernetesAndTemporalBeforeCurrent(t *testing.T) {
	cluster := &fakeCluster{}
	executor := &fakeExecutor{}
	cp, auth := newTestControlPlane(t, Config{RegistryAllowlist: []string{"registry.example.com"}, BootstrapEndpoint: "https://org.local/internal/bootstrap"}, cluster, executor)

	deployment, err := cp.PublishVersion(context.Background(), auth, workerVersionRequest("v1"))
	if err != nil {
		t.Fatal(err)
	}
	if deployment.State != domain.WorkerVersionReady {
		t.Fatalf("state = %q", deployment.State)
	}
	want := []string{"apply", "wait-kubernetes", "wait-temporal", "set-current"}
	got := append(cluster.calls, executor.calls...)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v, want %v", got, want)
	}
}

func TestPublishRejectsAnExistingImmutableVersionWithSpecificConflict(t *testing.T) {
	cp, auth := newTestControlPlane(t, Config{RegistryAllowlist: []string{"registry.example.com"}, BootstrapEndpoint: "https://org.local/internal/bootstrap"}, &fakeCluster{}, &fakeExecutor{})
	request := workerVersionRequest("v1")
	if _, err := cp.PublishVersion(context.Background(), auth, request); err != nil {
		t.Fatal(err)
	}
	if _, err := cp.PublishVersion(context.Background(), auth, request); !errors.Is(err, ErrWorkerVersionExists) || !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate version error = %v", err)
	}
}

func TestPublishWithoutContractStopsAwaitingBootstrapRegistrationBeforeTemporal(t *testing.T) {
	cluster := &fakeCluster{}
	executor := &fakeExecutor{}
	cp, auth := newTestControlPlane(t, Config{RegistryAllowlist: []string{"registry.example.com"}, BootstrapEndpoint: "https://org.local/internal/bootstrap"}, cluster, executor)
	request := workerVersionRequest("v1")
	request.ManifestDigest, request.Metadata = "", domain.WorkerMetadata{}
	version, err := cp.PublishVersion(context.Background(), auth, request)
	if err != nil {
		t.Fatal(err)
	}
	if version.State != domain.WorkerVersionPending || version.Current || version.RegistrationStatus != domain.BootstrapRegistrationAwaiting {
		t.Fatalf("pending version = %#v", version)
	}
	if got := strings.Join(append(cluster.calls, executor.calls...), ","); got != "apply,wait-kubernetes" {
		t.Fatalf("calls before registration = %s", got)
	}
}

func TestAcceptedBootstrapRegistrationRunsPinnedProbeBeforePromotion(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	cluster := &fakeCluster{}
	executor := &fakeExecutor{}
	cp, auth := newTestControlPlane(t, Config{
		RegistryAllowlist: []string{"registry.example.com"}, BootstrapTTL: 15 * time.Minute, Now: func() time.Time { return now },
		BootstrapEndpoint: "https://org.local/internal/bootstrap",
		BootstrapVerifier: BootstrapWorkloadVerifierFunc(func(_ context.Context, binding domain.BootstrapBinding, evidence BootstrapWorkloadEvidence) error {
			if binding.ExpectedImage != evidence.ObservedImage {
				return errors.New("image mismatch")
			}
			return nil
		}),
	}, cluster, executor)
	request := workerVersionRequest("v1")
	request.ManifestDigest, request.Metadata = "", domain.WorkerMetadata{}
	version, err := cp.PublishVersion(context.Background(), auth, request)
	if err != nil {
		t.Fatal(err)
	}
	material := cluster.bootstrap
	if material.Credential == "" || !strings.HasPrefix(material.Generation, "generation-") || material.Generation == "generation-1" {
		t.Fatalf("bootstrap credential/generation was not safely injected: %#v", material)
	}
	credentials := cp.store.BootstrapCredentials()
	if len(credentials) != 1 || credentials[0].Binding.DeploymentGeneration != material.Generation || credentials[0].Binding.ExpectedDeployment != version.KubernetesDeployment || credentials[0].Binding.TenantHash != version.TenantHash || credentials[0].Binding.VersionHash != version.VersionHash {
		t.Fatalf("bootstrap binding does not match candidate rollout: %#v", credentials)
	}
	contract := bootstrapContract(t, version.Version)
	executor.probeResult = RuntimeIdentity{ManifestDigest: contract.ManifestDigest, SDKModuleVersion: orgsdk.SDKModuleVersion, RuntimeProtocolVersion: orgsdk.RuntimeProtocolVersion, WorkerBuildID: version.Version}
	receipt, ready, err := cp.RegisterBootstrap(context.Background(), material.Credential, BootstrapWorkloadEvidence{ObservedImage: version.Image}, contract)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.ID == "" || ready.State != domain.WorkerVersionPending || ready.Current || len(executor.calls) != 0 {
		t.Fatalf("registration must return before polling: receipt=%#v version=%#v calls=%v", receipt, ready, executor.calls)
	}
	ready, err = cp.PromoteBootstrap(context.Background(), receipt)
	if err != nil || ready.State != domain.WorkerVersionReady || !ready.Current {
		t.Fatalf("promotion version=%#v error=%v", ready, err)
	}
	if got := strings.Join(executor.calls, ","); got != "wait-temporal,probe,set-current" {
		t.Fatalf("promotion calls = %s", got)
	}
	actions := map[string]bool{}
	outcomes := map[string]map[string]bool{}
	for _, audit := range cp.store.Audits(version.TenantID) {
		actions[audit.Action] = true
		if outcomes[audit.Action] == nil {
			outcomes[audit.Action] = map[string]bool{}
		}
		outcomes[audit.Action][audit.Outcome] = true
	}
	for _, action := range []string{"worker.version.promotion.waiting-for-poller", "worker.version.promotion.probing-contract", "worker.version.promotion.setting-current", "worker.version.promotion.succeeded"} {
		if !actions[action] {
			t.Fatalf("promotion Audit missing %q: %#v", action, cp.store.Audits(version.TenantID))
		}
	}
	if !outcomes["worker.version.promotion.waiting-for-poller"]["ready"] || !outcomes["worker.version.promotion.probing-contract"]["verified"] {
		t.Fatalf("promotion outcome Audits = %#v", cp.store.Audits(version.TenantID))
	}
}

func TestBootstrapPromotionFailureIsDurablyMarkedAndAudited(t *testing.T) {
	cluster := &fakeCluster{}
	executor := &fakeExecutor{probeErr: errors.New("probe unavailable")}
	cp, auth := newTestControlPlane(t, Config{
		RegistryAllowlist: []string{"registry.example.com"}, BootstrapEndpoint: "https://org.local/internal/bootstrap",
		BootstrapVerifier: BootstrapWorkloadVerifierFunc(func(context.Context, domain.BootstrapBinding, BootstrapWorkloadEvidence) error { return nil }),
	}, cluster, executor)
	request := workerVersionRequest("v1")
	request.ManifestDigest, request.Metadata = "", domain.WorkerMetadata{}
	version, err := cp.PublishVersion(context.Background(), auth, request)
	if err != nil {
		t.Fatal(err)
	}
	contract := bootstrapContract(t, version.Version)
	receipt, _, err := cp.RegisterBootstrap(context.Background(), cluster.bootstrap.Credential, BootstrapWorkloadEvidence{ObservedImage: version.Image}, contract)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cp.PromoteBootstrap(context.Background(), receipt); !errors.Is(err, executor.probeErr) {
		t.Fatalf("PromoteBootstrap error = %v", err)
	}
	stored, _ := cp.store.WorkerVersion(version.TenantID, version.WorkerName, version.Version)
	if stored.State != domain.WorkerVersionFailed || stored.PromotionPhase != domain.WorkerVersionPromotionFailed {
		t.Fatalf("failed promotion state = %#v", stored)
	}
	audits := cp.store.Audits(version.TenantID)
	if len(audits) == 0 || audits[len(audits)-1].Action != "worker.version.promotion.failed" || audits[len(audits)-1].Outcome != "failed" {
		t.Fatalf("failed promotion Audit = %#v", audits)
	}
	if audits[len(audits)-1].References["failedPhase"] != string(domain.WorkerVersionPromotionProbing) {
		t.Fatalf("probe failure phase Audit = %#v", audits)
	}
}

func TestPromotionControllerResumesAcceptedPendingVersionAfterRestart(t *testing.T) {
	now := time.Date(2026, 8, 1, 15, 0, 0, 0, time.UTC)
	cluster := &fakeCluster{}
	firstExecutor := &fakeExecutor{}
	cfg := Config{
		RegistryAllowlist: []string{"registry.example.com"}, BootstrapTTL: 15 * time.Minute, Now: func() time.Time { return now },
		BootstrapEndpoint: "https://org.local/internal/bootstrap",
		BootstrapVerifier: BootstrapWorkloadVerifierFunc(func(context.Context, domain.BootstrapBinding, BootstrapWorkloadEvidence) error { return nil }),
	}
	statePath := filepath.Join(t.TempDir(), "state.json")
	store, err := NewFileStore(statePath)
	if err != nil {
		t.Fatal(err)
	}
	tenant := testTenant("tenant-test", "test-tenant")
	if err := store.SaveTenant(tenant); err != nil {
		t.Fatal(err)
	}
	first := New(cfg, store, cluster, firstExecutor)
	auth := authFor(tenant)
	if _, err := first.CreateWorker(context.Background(), auth, CreateWorkerRequest{WorkerName: "payments-worker"}); err != nil {
		t.Fatal(err)
	}
	request := workerVersionRequest("v1")
	request.ManifestDigest, request.Metadata = "", domain.WorkerMetadata{}
	version, err := first.PublishVersion(context.Background(), auth, request)
	if err != nil {
		t.Fatal(err)
	}
	contract := bootstrapContract(t, version.Version)
	if _, _, err := first.RegisterBootstrap(context.Background(), cluster.bootstrap.Credential, BootstrapWorkloadEvidence{ObservedImage: version.Image}, contract); err != nil {
		t.Fatal(err)
	}

	promoted := make(chan struct{}, 1)
	restartedExecutor := &fakeExecutor{probeResult: RuntimeIdentity{ManifestDigest: contract.ManifestDigest, SDKModuleVersion: orgsdk.SDKModuleVersion, RuntimeProtocolVersion: orgsdk.RuntimeProtocolVersion, WorkerBuildID: version.Version}, setCurrent: func(domain.WorkerVersion) { promoted <- struct{}{} }}
	reopened, err := NewFileStore(statePath)
	if err != nil {
		t.Fatal(err)
	}
	restarted := New(cfg, reopened, cluster, restartedExecutor)
	ctx, cancel := context.WithCancel(context.Background())
	if err := restarted.StartBootstrapPromotionController(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-promoted:
	case <-time.After(time.Second):
		t.Fatal("accepted pending promotion was not resumed")
	}
	cancel()
	if err := restarted.WaitBootstrapPromotionController(); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	stored, _ := reopened.WorkerVersion(version.TenantID, version.WorkerName, version.Version)
	if stored.State != domain.WorkerVersionReady || !stored.Current || stored.PromotionAttemptID == "" {
		t.Fatalf("resumed version = %#v", stored)
	}
}

func TestPromotionControllerRetriesAfterFinalStatePersistenceFailure(t *testing.T) {
	cluster := &fakeCluster{}
	executor := &fakeExecutor{}
	base := NewMemoryStore()
	tenant := testTenant("tenant-test", "test-tenant")
	if err := base.SaveTenant(tenant); err != nil {
		t.Fatal(err)
	}
	store := &promotionCommitFaultStore{Store: base, failSucceededOnce: true}
	cp := New(Config{
		RegistryAllowlist: []string{"registry.example.com"}, BootstrapEndpoint: "https://org.local/internal/bootstrap",
		BootstrapVerifier: BootstrapWorkloadVerifierFunc(func(context.Context, domain.BootstrapBinding, BootstrapWorkloadEvidence) error { return nil }),
	}, store, cluster, executor)
	auth := authFor(tenant)
	if _, err := cp.CreateWorker(context.Background(), auth, CreateWorkerRequest{WorkerName: "payments-worker"}); err != nil {
		t.Fatal(err)
	}
	request := workerVersionRequest("v1")
	request.ManifestDigest, request.Metadata = "", domain.WorkerMetadata{}
	version, err := cp.PublishVersion(context.Background(), auth, request)
	if err != nil {
		t.Fatal(err)
	}
	contract := bootstrapContract(t, version.Version)
	if _, _, err := cp.RegisterBootstrap(context.Background(), cluster.bootstrap.Credential, BootstrapWorkloadEvidence{ObservedImage: version.Image}, contract); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := cp.StartBootstrapPromotionController(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(2 * time.Second)
	for {
		stored, _ := base.WorkerVersion(version.TenantID, version.WorkerName, version.Version)
		if stored.State == domain.WorkerVersionReady {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("promotion did not recover from persistence failure: %#v", stored)
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	if err := cp.WaitBootstrapPromotionController(); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	setCurrentCalls := 0
	for _, call := range executor.calls {
		if call == "set-current" {
			setCurrentCalls++
		}
	}
	if setCurrentCalls != 2 {
		t.Fatalf("SetCurrent calls = %d, want one retried call after the lost local commit", setCurrentCalls)
	}
	retryAudited := false
	for _, audit := range base.Audits(version.TenantID) {
		if audit.Action == "worker.version.promotion.setting-current" && audit.Outcome == "retrying" {
			retryAudited = true
		}
	}
	if !retryAudited {
		t.Fatalf("promotion retry Audit missing: %#v", base.Audits(version.TenantID))
	}
}

func TestPromotionControllerSingleflightsConcurrentExactReceiptRetries(t *testing.T) {
	cluster := &fakeCluster{}
	entered, release := make(chan struct{}), make(chan struct{})
	executor := &fakeExecutor{waitForPoller: func() error {
		close(entered)
		<-release
		return nil
	}}
	cp, auth := newTestControlPlane(t, Config{
		RegistryAllowlist: []string{"registry.example.com"}, BootstrapEndpoint: "https://org.local/internal/bootstrap",
		BootstrapVerifier: BootstrapWorkloadVerifierFunc(func(context.Context, domain.BootstrapBinding, BootstrapWorkloadEvidence) error { return nil }),
	}, cluster, executor)
	request := workerVersionRequest("v1")
	request.ManifestDigest, request.Metadata = "", domain.WorkerMetadata{}
	version, err := cp.PublishVersion(context.Background(), auth, request)
	if err != nil {
		t.Fatal(err)
	}
	receipt, _, err := cp.RegisterBootstrap(context.Background(), cluster.bootstrap.Credential, BootstrapWorkloadEvidence{ObservedImage: version.Image}, bootstrapContract(t, version.Version))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := cp.StartBootstrapPromotionController(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("promotion did not enter poller wait")
	}
	if err := cp.ScheduleBootstrapPromotion(ctx, receipt); err != nil {
		t.Fatal(err)
	}
	if err := cp.ScheduleBootstrapPromotion(ctx, receipt); err != nil {
		t.Fatal(err)
	}
	close(release)
	deadline := time.After(time.Second)
	for {
		stored, _ := cp.store.WorkerVersion(version.TenantID, version.WorkerName, version.Version)
		if stored.State == domain.WorkerVersionReady {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("promotion did not finish: %#v", stored)
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	if err := cp.WaitBootstrapPromotionController(); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if got := strings.Count(strings.Join(executor.calls, ","), "wait-temporal"); got != 1 {
		t.Fatalf("poller waits = %d, want one singleflight promotion: %v", got, executor.calls)
	}
}

type promotionCommitFaultStore struct {
	Store
	failSucceededOnce bool
}

func (s *promotionCommitFaultStore) CommitWorkerVersionAudit(tenantID string, version domain.WorkerVersion, audit domain.AuditRecord) error {
	if s.failSucceededOnce && version.PromotionPhase == domain.WorkerVersionPromotionSucceeded {
		s.failSucceededOnce = false
		return errors.New("injected final promotion persistence failure")
	}
	return s.Store.CommitWorkerVersionAudit(tenantID, version, audit)
}

func (s *promotionCommitFaultStore) CommitCurrentWorkerVersion(tenantID string, worker domain.Worker, version domain.WorkerVersion, audit *domain.AuditRecord) error {
	if s.failSucceededOnce && version.PromotionPhase == domain.WorkerVersionPromotionSucceeded {
		s.failSucceededOnce = false
		return errors.New("injected final promotion persistence failure")
	}
	return s.Store.CommitCurrentWorkerVersion(tenantID, worker, version, audit)
}

func TestRegistrationDuringKubernetesReadinessDoesNotLoseAcceptedContract(t *testing.T) {
	cluster := &fakeCluster{}
	var cp *ControlPlane
	var contract domain.WorkerContractRegistration
	cluster.waitReady = func(version domain.WorkerVersion) error {
		contract = bootstrapContract(t, version.Version)
		_, _, err := cp.RegisterBootstrap(context.Background(), cluster.bootstrap.Credential, BootstrapWorkloadEvidence{ObservedImage: version.Image}, contract)
		return err
	}
	cp, auth := newTestControlPlane(t, Config{RegistryAllowlist: []string{"registry.example.com"}, BootstrapEndpoint: "https://org.local/internal/bootstrap", BootstrapVerifier: BootstrapWorkloadVerifierFunc(func(context.Context, domain.BootstrapBinding, BootstrapWorkloadEvidence) error { return nil })}, cluster, &fakeExecutor{})
	request := workerVersionRequest("v1")
	request.ManifestDigest, request.Metadata = "", domain.WorkerMetadata{}
	version, err := cp.PublishVersion(context.Background(), auth, request)
	if err != nil {
		t.Fatal(err)
	}
	if version.RegistrationStatus != domain.BootstrapRegistrationAccepted || version.ManifestDigest != contract.ManifestDigest || len(version.Metadata.Workflows) == 0 || !version.Health.KubernetesReady {
		t.Fatalf("accepted registration was overwritten: %#v", version)
	}
}

func TestDeployOrgSDKVersionVerifiesRuntimeIdentityBeforeCurrent(t *testing.T) {
	definition := dynamicServiceDefinition()
	request := dynamicServiceWorkerVersionRequest(t, definition)
	cluster := &fakeCluster{}
	executor := &fakeExecutor{probeResult: RuntimeIdentity{
		ManifestDigest: request.ManifestDigest, SDKModuleVersion: orgsdk.SDKModuleVersion,
		RuntimeProtocolVersion: orgsdk.RuntimeProtocolVersion, WorkerBuildID: request.Version,
	}}
	cp, auth := newTestControlPlane(t, Config{RegistryAllowlist: []string{"registry.example.com"}}, cluster, executor)
	deployment, err := cp.PublishVersion(context.Background(), auth, request)
	if err != nil {
		t.Fatal(err)
	}
	if deployment.State != domain.WorkerVersionReady {
		t.Fatalf("state = %q", deployment.State)
	}
	want := []string{"apply", "wait-kubernetes", "wait-temporal", "probe", "set-current"}
	got := append(cluster.calls, executor.calls...)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v, want %v", got, want)
	}
}

func TestDeployOrgSDKVersionFailsOnRuntimeIdentityMismatch(t *testing.T) {
	definition := dynamicServiceDefinition()
	request := dynamicServiceWorkerVersionRequest(t, definition)
	executor := &fakeExecutor{probeResult: RuntimeIdentity{
		ManifestDigest: "sha256:" + strings.Repeat("f", 64), SDKModuleVersion: orgsdk.SDKModuleVersion,
		RuntimeProtocolVersion: orgsdk.RuntimeProtocolVersion, WorkerBuildID: request.Version,
	}}
	cp, auth := newTestControlPlane(t, Config{RegistryAllowlist: []string{"registry.example.com"}}, &fakeCluster{}, executor)
	deployment, err := cp.PublishVersion(context.Background(), auth, request)
	if err == nil || !strings.Contains(err.Error(), "manifest digest") || deployment.State != domain.WorkerVersionFailed {
		t.Fatalf("deployment=%#v error=%v", deployment, err)
	}
	if strings.Contains(strings.Join(executor.calls, ","), "set-current") {
		t.Fatalf("mismatched version was promoted: %v", executor.calls)
	}
}

func TestStartUsesCurrentOrExplicitReadyVersion(t *testing.T) {
	executor := &fakeExecutor{}
	cp, auth := newTestControlPlane(t, Config{RegistryAllowlist: []string{"registry.example.com"}}, &fakeCluster{}, executor)
	if _, err := cp.PublishVersion(context.Background(), auth, workerVersionRequest("v1")); err != nil {
		t.Fatal(err)
	}
	if _, err := cp.PublishVersion(context.Background(), auth, workerVersionRequest("v2")); err != nil {
		t.Fatal(err)
	}

	current, err := cp.Start(context.Background(), auth, StartRequest{WorkerName: "payments-worker", Workflow: "ChargeOrder", Input: []byte(`{"order":"42"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if current.SelectedVersion != "v2" || executor.starts[0].PinnedVersion != "v2" {
		t.Fatalf("current start = %#v / %#v", current, executor.starts[0])
	}

	historical, err := cp.Start(context.Background(), auth, StartRequest{WorkerName: "payments-worker", Workflow: "ChargeOrder", WorkerVersion: "v1", Input: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if historical.SelectedVersion != "v1" || executor.starts[1].PinnedVersion != "v1" {
		t.Fatalf("historical start = %#v / %#v", historical, executor.starts[1])
	}
}

func TestDefaultStartSerializesWithPromotionAndPinsResolvedCurrent(t *testing.T) {
	cluster := &fakeCluster{}
	executor := &fakeExecutor{}
	cp, auth := newTestControlPlane(t, Config{RegistryAllowlist: []string{"registry.example.com"}}, cluster, executor)
	if _, err := cp.PublishVersion(context.Background(), auth, workerVersionRequest("v1")); err != nil {
		t.Fatal(err)
	}

	publishOutsideLock := make(chan struct{})
	releaseDeployment := make(chan struct{})
	cluster.apply = func(version domain.WorkerVersion) error {
		if version.Version == "v2" {
			close(publishOutsideLock)
			<-releaseDeployment
		}
		return nil
	}
	startEntered := make(chan struct{})
	releaseStart := make(chan struct{})
	executor.start = func(start ExecutionStart) (string, error) {
		close(startEntered)
		<-releaseStart
		return "run-" + start.InvocationID, nil
	}
	v2SetCurrent := make(chan struct{}, 1)
	executor.setCurrent = func(version domain.WorkerVersion) {
		if version.Version == "v2" {
			v2SetCurrent <- struct{}{}
		}
	}

	publishResult := make(chan error, 1)
	go func() {
		_, err := cp.PublishVersion(context.Background(), auth, workerVersionRequest("v2"))
		publishResult <- err
	}()
	select {
	case <-publishOutsideLock:
	case <-time.After(time.Second):
		t.Fatal("v2 publish did not reach the unlocked deployment phase")
	}
	startResult := make(chan domain.Invocation, 1)
	startErr := make(chan error, 1)
	go func() {
		invocation, err := cp.Start(context.Background(), auth, StartRequest{WorkerName: "payments-worker", Workflow: "ChargeOrder", Input: []byte(`{}`)})
		startResult <- invocation
		startErr <- err
	}()
	select {
	case <-startEntered:
	case <-time.After(time.Second):
		t.Fatal("default start did not resolve Current")
	}
	close(releaseDeployment)
	select {
	case <-v2SetCurrent:
		t.Fatal("promotion changed Temporal Current while the resolved default start was in flight")
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseStart)
	invocation := <-startResult
	if err := <-startErr; err != nil {
		t.Fatal(err)
	}
	if err := <-publishResult; err != nil {
		t.Fatal(err)
	}
	if invocation.SelectedVersion != "v1" || len(executor.starts) == 0 || executor.starts[0].PinnedVersion != "v1" {
		t.Fatalf("resolved start = %#v starts=%#v", invocation, executor.starts)
	}
}

func TestStartPersistsInvocationReservationAndQuotaBeforeTemporal(t *testing.T) {
	store := NewMemoryStore()
	tenant := testTenant("tenant-test", "test-tenant")
	if err := store.SaveTenant(tenant); err != nil {
		t.Fatal(err)
	}
	executor := &fakeExecutor{}
	executor.start = func(start ExecutionStart) (string, error) {
		invocation, ok := store.Invocation(tenant.ID, start.InvocationID)
		if !ok || invocation.TemporalWorkflowID != start.WorkflowID || invocation.SelectedVersion != start.PinnedVersion {
			return "", errors.New("durable invocation reservation was not visible before Temporal start")
		}
		leases := store.QuotaLeases(tenant.ID)
		if len(leases) != 2 { // one active release plus this run reservation
			return "", fmt.Errorf("durable run quota reservation was not visible before Temporal start: %#v", leases)
		}
		return "run-" + start.InvocationID, nil
	}
	cp := New(Config{RegistryAllowlist: []string{"registry.example.com"}}, store, &fakeCluster{}, executor)
	auth := authFor(tenant)
	if _, err := cp.CreateWorker(context.Background(), auth, CreateWorkerRequest{WorkerName: "payments-worker"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cp.PublishVersion(context.Background(), auth, workerVersionRequest("v1")); err != nil {
		t.Fatal(err)
	}
	if _, err := cp.Start(context.Background(), auth, StartRequest{WorkerName: "payments-worker", Workflow: "ChargeOrder", Input: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
}

func TestInvocationReconcilerRecoversStartAfterTemporalSuccessAndLocalCommitFailure(t *testing.T) {
	base := NewMemoryStore()
	tenant := testTenant("tenant-test", "test-tenant")
	if err := base.SaveTenant(tenant); err != nil {
		t.Fatal(err)
	}
	store := &invocationRunningFaultStore{Store: base, failRunningOnce: true}
	executor := &fakeExecutor{}
	cp := New(Config{RegistryAllowlist: []string{"registry.example.com"}}, store, &fakeCluster{}, executor)
	auth := authFor(tenant)
	if _, err := cp.CreateWorker(context.Background(), auth, CreateWorkerRequest{WorkerName: "payments-worker"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cp.PublishVersion(context.Background(), auth, workerVersionRequest("v1")); err != nil {
		t.Fatal(err)
	}
	reserved, err := cp.Start(context.Background(), auth, StartRequest{WorkerName: "payments-worker", Workflow: "ChargeOrder", Input: []byte(`{}`)})
	if !errors.Is(err, errInjectedInvocationRunningCommit) {
		t.Fatalf("Start reservation=%#v error=%v", reserved, err)
	}
	stored, ok := base.Invocation(tenant.ID, reserved.ID)
	if !ok || stored.State != domain.InvocationStarting || stored.TemporalRunID != "" {
		t.Fatalf("durable start reservation = %#v", stored)
	}
	if err := cp.ReconcileInvocations(context.Background()); err != nil {
		t.Fatal(err)
	}
	stored, _ = base.Invocation(tenant.ID, reserved.ID)
	if stored.State != domain.InvocationRunning || stored.TemporalRunID == "" || len(executor.starts) != 2 {
		t.Fatalf("reconciled invocation=%#v starts=%#v", stored, executor.starts)
	}
}

func TestInvocationReconcilerPersistsTerminalStateAndReleasesQuotaWithoutRead(t *testing.T) {
	store := NewMemoryStore()
	tenant := testTenant("tenant-terminal", "terminal")
	if err := store.SaveTenant(tenant); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	invocation := domain.Invocation{
		ID: "inv-terminal", TenantID: tenant.ID, TenantSlug: tenant.Slug, WorkerName: "worker", Workflow: "Workflow", SelectedVersion: "v1",
		TemporalWorkflowID: "workflow-terminal", TemporalRunID: "run-terminal", State: domain.InvocationRunning, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveInvocation(tenant.ID, invocation); err != nil {
		t.Fatal(err)
	}
	if err := store.AcquireQuotaLease(tenant.ID, domain.QuotaLease{ID: "run:" + invocation.ID, TenantID: tenant.ID, Kind: domain.QuotaLeaseRun, ConcurrentRuns: 1, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	cp := New(Config{}, store, &fakeCluster{}, &fakeExecutor{describeResult: ExecutionState{Status: "completed"}})
	if err := cp.ReconcileInvocations(context.Background()); err != nil {
		t.Fatal(err)
	}
	stored, _ := store.Invocation(tenant.ID, invocation.ID)
	if stored.State != domain.InvocationCompleted || len(store.QuotaLeases(tenant.ID)) != 0 {
		t.Fatalf("terminal invocation=%#v leases=%#v", stored, store.QuotaLeases(tenant.ID))
	}
}

func TestInvocationReconcilerControllerResumesDurableStartsAfterRestart(t *testing.T) {
	store := NewMemoryStore()
	tenant := testTenant("tenant-restart", "restart")
	if err := store.SaveTenant(tenant); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	invocation := domain.Invocation{
		ID: "inv-restart", TenantID: tenant.ID, TenantSlug: tenant.Slug, WorkerName: "worker", Workflow: "Workflow", SelectedVersion: "v1",
		TaskQueue: "queue", WorkerDeployment: "deployment", TemporalWorkflowID: "workflow-restart", State: domain.InvocationStarting, CreatedAt: now, UpdatedAt: now,
	}
	lease := domain.QuotaLease{ID: "run:" + invocation.ID, TenantID: tenant.ID, Kind: domain.QuotaLeaseRun, ConcurrentRuns: 1, CreatedAt: now}
	if err := store.CommitInvocationReservation(tenant.ID, invocation, lease); err != nil {
		t.Fatal(err)
	}
	cp := New(Config{InvocationReconcileInterval: 10 * time.Millisecond}, store, &fakeCluster{}, &fakeExecutor{})
	ctx, cancel := context.WithCancel(context.Background())
	if err := cp.StartInvocationReconciler(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(time.Second)
	for {
		stored, _ := store.Invocation(tenant.ID, invocation.ID)
		if stored.State == domain.InvocationRunning {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("durable start was not resumed: %#v", stored)
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	if err := cp.WaitInvocationReconciler(); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestInvocationReconcilerDoesNotRaceAnInFlightForegroundStart(t *testing.T) {
	store := NewMemoryStore()
	tenant := testTenant("tenant-inflight", "inflight")
	if err := store.SaveTenant(tenant); err != nil {
		t.Fatal(err)
	}
	var starts atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	executor := &fakeExecutor{start: func(start ExecutionStart) (string, error) {
		if starts.Add(1) == 1 {
			close(entered)
			<-release
		}
		return "run-" + start.InvocationID, nil
	}}
	cp := New(Config{RegistryAllowlist: []string{"registry.example.com"}, InvocationReconcileInterval: 10 * time.Millisecond}, store, &fakeCluster{}, executor)
	auth := authFor(tenant)
	if _, err := cp.CreateWorker(context.Background(), auth, CreateWorkerRequest{WorkerName: "payments-worker"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cp.PublishVersion(context.Background(), auth, workerVersionRequest("v1")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := cp.StartInvocationReconciler(ctx); err != nil {
		t.Fatal(err)
	}
	startDone := make(chan error, 1)
	go func() {
		_, err := cp.Start(context.Background(), auth, StartRequest{WorkerName: "payments-worker", Workflow: "ChargeOrder", Input: []byte(`{}`)})
		startDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("foreground start did not reach Temporal")
	}
	time.Sleep(50 * time.Millisecond)
	if got := starts.Load(); got != 1 {
		t.Fatalf("Temporal starts while foreground request was in flight = %d", got)
	}
	close(release)
	if err := <-startDone; err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := cp.WaitInvocationReconciler(); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestBackgroundReconcilerRemovesFailedReleaseAndFinishedDeploymentLeases(t *testing.T) {
	store := NewMemoryStore()
	tenant := testTenant("tenant-stale", "stale")
	if err := store.SaveTenant(tenant); err != nil {
		t.Fatal(err)
	}
	version := domain.WorkerVersion{ID: "ver-failed", TenantID: tenant.ID, TenantSlug: tenant.Slug, WorkerName: "worker", Version: "v1", State: domain.WorkerVersionFailed}
	if err := store.SaveWorkerVersion(tenant.ID, version); err != nil {
		t.Fatal(err)
	}
	for _, lease := range []domain.QuotaLease{
		{ID: "release:" + version.ID, TenantID: tenant.ID, Kind: domain.QuotaLeaseRelease, ActiveReleases: 1},
		{ID: "deployment:" + version.ID, TenantID: tenant.ID, Kind: domain.QuotaLeaseDeployment, ConcurrentDeployments: 1},
	} {
		if err := store.AcquireQuotaLease(tenant.ID, lease); err != nil {
			t.Fatal(err)
		}
	}
	cp := New(Config{}, store, &fakeCluster{}, &fakeExecutor{})
	if err := cp.ReconcileInvocations(context.Background()); err != nil {
		t.Fatal(err)
	}
	if leases := store.QuotaLeases(tenant.ID); len(leases) != 0 {
		t.Fatalf("stale failed-release leases were not reconciled: %#v", leases)
	}
}

var errInjectedInvocationRunningCommit = errors.New("injected invocation running commit failure")

type invocationRunningFaultStore struct {
	Store
	failRunningOnce bool
}

func (s *invocationRunningFaultStore) SaveInvocation(tenantID string, invocation domain.Invocation) error {
	if s.failRunningOnce && invocation.State == domain.InvocationRunning {
		s.failRunningOnce = false
		return errInjectedInvocationRunningCommit
	}
	return s.Store.SaveInvocation(tenantID, invocation)
}

func TestWorkerViewContainsCurrentAndVersionDescriptions(t *testing.T) {
	cp, auth := newTestControlPlane(t, Config{RegistryAllowlist: []string{"registry.example.com"}}, &fakeCluster{}, &fakeExecutor{})
	if _, err := cp.PublishVersion(context.Background(), auth, workerVersionRequest("v1")); err != nil {
		t.Fatal(err)
	}
	if _, err := cp.PublishVersion(context.Background(), auth, workerVersionRequest("v2")); err != nil {
		t.Fatal(err)
	}
	view, err := cp.GetWorker(context.Background(), auth, "payments-worker")
	if err != nil {
		t.Fatal(err)
	}
	if view.Worker.CurrentVersion != "v2" || len(view.Versions) != 2 || view.Versions[0].Description == "" || view.Versions[1].Description == "" {
		t.Fatalf("worker view = %#v", view)
	}
}

func TestStartRejectsUndeclaredWorkflow(t *testing.T) {
	cp, auth := newTestControlPlane(t, Config{RegistryAllowlist: []string{"registry.example.com"}}, &fakeCluster{}, &fakeExecutor{})
	if _, err := cp.PublishVersion(context.Background(), auth, workerVersionRequest("v1")); err != nil {
		t.Fatal(err)
	}
	if _, err := cp.Start(context.Background(), auth, StartRequest{WorkerName: "payments-worker", Workflow: "HiddenWorkflow", Input: []byte(`{}`)}); err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("expected declaration error, got %v", err)
	}
}

func TestStartIdempotencyKeyAvoidsAccidentalDuplicate(t *testing.T) {
	executor := &fakeExecutor{}
	cp, auth := newTestControlPlane(t, Config{RegistryAllowlist: []string{"registry.example.com"}}, &fakeCluster{}, executor)
	if _, err := cp.PublishVersion(context.Background(), auth, workerVersionRequest("v1")); err != nil {
		t.Fatal(err)
	}
	req := StartRequest{WorkerName: "payments-worker", Workflow: "ChargeOrder", IdempotencyKey: "checkout-42", Input: []byte(`{}`)}
	first, err := cp.Start(context.Background(), auth, req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := cp.Start(context.Background(), auth, req)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || len(executor.starts) != 1 {
		t.Fatalf("duplicate start: %q %q calls=%d", first.ID, second.ID, len(executor.starts))
	}
}

func TestGetInvocationUsesDeclaredProjectionQueryAndBuildsDiagnosticsLink(t *testing.T) {
	executor := &fakeExecutor{queryResult: []byte(`{"steps":[{"id":"charge","label":"Charge","status":"running"}],"currentStep":"charge","status":"waiting","blockReason":"bank approval","allowedActions":["cancel"]}`)}
	cp, auth := newTestControlPlane(t, Config{RegistryAllowlist: []string{"registry.example.com"}, TemporalWebBaseURL: "http://localhost:8080", TemporalNamespace: "default"}, &fakeCluster{}, executor)
	auth.Permissions[PermissionDiagnosticsRead] = true
	if _, err := cp.PublishVersion(context.Background(), auth, workerVersionRequest("v1")); err != nil {
		t.Fatal(err)
	}
	inv, err := cp.Start(context.Background(), auth, StartRequest{WorkerName: "payments-worker", Workflow: "ChargeOrder", Input: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	view, err := cp.GetInvocation(context.Background(), auth, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if executor.lastQuery != "org_projection" || view.Projection.CurrentStep != "charge" || view.Projection.BlockReason != "bank approval" {
		t.Fatalf("view = %#v query=%q", view, executor.lastQuery)
	}
	if !strings.Contains(view.TemporalDiagnosticsURL, "/namespaces/default/workflows/"+inv.TemporalWorkflowID+"/") {
		t.Fatalf("diagnostics URL = %q", view.TemporalDiagnosticsURL)
	}
}

func TestGetInvocationReturnsValidatedOrgSDKDynamicProjection(t *testing.T) {
	definition := dynamicServiceDefinition()
	graph, err := orgsdk.NewGraph(definition, "DynamicWorkflow", "v1", time.Unix(1, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	nodeID, err := graph.CreateNode("route", "", "singleton", nil, time.Unix(2, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.Transition(nodeID, orgsdk.NodeStatusRunning, "", time.Unix(3, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	projectionJSON, err := json.Marshal(graph.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	executor := &fakeExecutor{queryResult: projectionJSON}
	cp, auth := newTestControlPlane(t, Config{RegistryAllowlist: []string{"registry.example.com"}}, &fakeCluster{}, executor)
	if _, err := cp.PublishVersion(context.Background(), auth, dynamicServiceWorkerVersionRequest(t, definition)); err != nil {
		t.Fatal(err)
	}
	invocation, err := cp.Start(context.Background(), auth, StartRequest{WorkerName: "payments-worker", Workflow: "DynamicWorkflow", Input: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	view, err := cp.GetInvocation(context.Background(), auth, invocation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.SemanticProjection == nil || view.SemanticProjection.Nodes[0].RuntimeNodeID != nodeID || view.Projection.Status != "" {
		t.Fatalf("dynamic invocation view = %#v", view)
	}
}

func TestGetInvocationRejectsDynamicProjectionOutsideManifest(t *testing.T) {
	definition := dynamicServiceDefinition()
	projection := orgsdk.Projection{
		ContractVersion: orgsdk.ContractVersion, WorkflowName: "DynamicWorkflow", WorkerVersion: "v1", Revision: 2, Status: "running",
		Nodes:          []orgsdk.NodeProjection{{RuntimeNodeID: "hidden-aaaaaaaaaaaaaaaa", TemplateID: "hidden", Label: "Hidden", Status: orgsdk.NodeStatusRunning}},
		CurrentNodeIDs: []string{"hidden-aaaaaaaaaaaaaaaa"},
	}
	projectionJSON, err := json.Marshal(projection)
	if err != nil {
		t.Fatal(err)
	}
	executor := &fakeExecutor{queryResult: projectionJSON}
	cp, auth := newTestControlPlane(t, Config{RegistryAllowlist: []string{"registry.example.com"}}, &fakeCluster{}, executor)
	if _, err := cp.PublishVersion(context.Background(), auth, dynamicServiceWorkerVersionRequest(t, definition)); err != nil {
		t.Fatal(err)
	}
	invocation, err := cp.Start(context.Background(), auth, StartRequest{WorkerName: "payments-worker", Workflow: "DynamicWorkflow", Input: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cp.GetInvocation(context.Background(), auth, invocation.ID); err == nil || !strings.Contains(err.Error(), "undeclared template") {
		t.Fatalf("invalid dynamic projection error = %v", err)
	}
}

func TestSignalAndQueryMustBeDeclared(t *testing.T) {
	req := workerVersionRequest("v1")
	req.Metadata.Workflows[0].Signals = []domain.Operation{{Name: "approve"}}
	req.Metadata.Workflows[0].Queries = []domain.Operation{{Name: "summary"}}
	cp, auth := newTestControlPlane(t, Config{RegistryAllowlist: []string{"registry.example.com"}}, &fakeCluster{}, &fakeExecutor{})
	if _, err := cp.PublishVersion(context.Background(), auth, req); err != nil {
		t.Fatal(err)
	}
	inv, err := cp.Start(context.Background(), auth, StartRequest{WorkerName: "payments-worker", Workflow: "ChargeOrder", Input: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := cp.Signal(context.Background(), auth, inv.ID, "hidden", []byte(`{}`)); err == nil {
		t.Fatal("expected undeclared signal rejection")
	}
	if _, err := cp.Query(context.Background(), auth, inv.ID, "hidden", nil); err == nil {
		t.Fatal("expected undeclared query rejection")
	}
	if err := cp.Signal(context.Background(), auth, inv.ID, "approve", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := cp.Query(context.Background(), auth, inv.ID, "summary", nil); err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowStartSchemaRejectsBeforeQuotaAndTemporal(t *testing.T) {
	req := workerVersionRequest("v1")
	req.Metadata.Workflows[0].InputSchema = json.RawMessage(`{"type":"object","required":["order"],"properties":{"order":{"type":"string"}},"additionalProperties":false}`)
	executor := &fakeExecutor{}
	cp, auth := newTestControlPlane(t, Config{RegistryAllowlist: []string{"registry.example.com"}}, &fakeCluster{}, executor)
	if _, err := cp.PublishVersion(context.Background(), auth, req); err != nil {
		t.Fatal(err)
	}
	leasesBefore := len(cp.store.QuotaLeases(auth.TenantID))
	if _, err := cp.Start(context.Background(), auth, StartRequest{WorkerName: "payments-worker", Workflow: "ChargeOrder", Input: []byte(`{}`)}); err == nil || !strings.Contains(err.Error(), "input schema") {
		t.Fatalf("invalid start schema error = %v", err)
	}
	if len(executor.starts) != 0 || len(cp.store.QuotaLeases(auth.TenantID)) != leasesBefore || len(cp.store.Invocations(auth.TenantID)) != 0 {
		t.Fatalf("invalid start reached mutation/adapters: starts=%#v leases=%#v invocations=%#v", executor.starts, cp.store.QuotaLeases(auth.TenantID), cp.store.Invocations(auth.TenantID))
	}
}

func TestSignalAndQuerySchemasRejectBeforeExecutor(t *testing.T) {
	req := workerVersionRequest("v1")
	schema := json.RawMessage(`{"type":"object","required":["value"],"properties":{"value":{"type":"string"}},"additionalProperties":false}`)
	req.Metadata.Workflows[0].Signals = []domain.Operation{{Name: "approve", InputSchema: schema}}
	req.Metadata.Workflows[0].Queries = []domain.Operation{{Name: "summary", InputSchema: schema}}
	executor := &fakeExecutor{}
	cp, auth := newTestControlPlane(t, Config{RegistryAllowlist: []string{"registry.example.com"}}, &fakeCluster{}, executor)
	if _, err := cp.PublishVersion(context.Background(), auth, req); err != nil {
		t.Fatal(err)
	}
	invocation, err := cp.Start(context.Background(), auth, StartRequest{WorkerName: "payments-worker", Workflow: "ChargeOrder", Input: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := cp.Signal(context.Background(), auth, invocation.ID, "approve", []byte(`{}`)); err == nil || !strings.Contains(err.Error(), "input schema") {
		t.Fatalf("invalid signal schema error = %v", err)
	}
	if _, err := cp.Query(context.Background(), auth, invocation.ID, "summary", []byte(`{}`)); err == nil || !strings.Contains(err.Error(), "input schema") {
		t.Fatalf("invalid query schema error = %v", err)
	}
	if len(executor.signals) != 0 || executor.lastQuery != "" {
		t.Fatalf("invalid operation input reached executor: signals=%#v query=%q", executor.signals, executor.lastQuery)
	}
}

type fakeCluster struct {
	calls     []string
	bootstrap BootstrapDeployment
	waitReady func(domain.WorkerVersion) error
	apply     func(domain.WorkerVersion) error
}

func (f *fakeCluster) Apply(_ context.Context, version domain.WorkerVersion) error {
	f.calls = append(f.calls, "apply")
	if f.apply != nil {
		return f.apply(version)
	}
	return nil
}

func (f *fakeCluster) ApplyBootstrap(_ context.Context, _ domain.WorkerVersion, deployment BootstrapDeployment) error {
	f.calls = append(f.calls, "apply")
	f.bootstrap = deployment
	return nil
}
func (f *fakeCluster) WaitReady(_ context.Context, version domain.WorkerVersion) error {
	f.calls = append(f.calls, "wait-kubernetes")
	if f.waitReady != nil {
		return f.waitReady(version)
	}
	return nil
}

type fakeExecutor struct {
	calls          []string
	starts         []ExecutionStart
	queryResult    []byte
	lastQuery      string
	signals        []fakeSignal
	signalErr      error
	probeResult    RuntimeIdentity
	probeErr       error
	setCurrent     func(domain.WorkerVersion)
	waitForPoller  func() error
	start          func(ExecutionStart) (string, error)
	describeResult ExecutionState
	describeErr    error
}

type fakeSignal struct {
	name  string
	input []byte
}

func (f *fakeExecutor) WaitForPoller(context.Context, domain.WorkerVersion) error {
	f.calls = append(f.calls, "wait-temporal")
	if f.waitForPoller != nil {
		return f.waitForPoller()
	}
	return nil
}
func (f *fakeExecutor) Probe(_ context.Context, version domain.WorkerVersion) (RuntimeIdentity, error) {
	f.calls = append(f.calls, "probe")
	if f.probeResult == (RuntimeIdentity{}) && f.probeErr == nil {
		return RuntimeIdentity{
			ManifestDigest: version.ManifestDigest, SDKModuleVersion: version.Metadata.SDK.ModuleVersion,
			RuntimeProtocolVersion: version.Metadata.SDK.RuntimeProtocolVersion, WorkerBuildID: version.Version,
		}, nil
	}
	return f.probeResult, f.probeErr
}
func (f *fakeExecutor) SetCurrent(_ context.Context, version domain.WorkerVersion) error {
	f.calls = append(f.calls, "set-current")
	if f.setCurrent != nil {
		f.setCurrent(version)
	}
	return nil
}
func (f *fakeExecutor) Start(_ context.Context, s ExecutionStart) (string, error) {
	f.starts = append(f.starts, s)
	if f.start != nil {
		return f.start(s)
	}
	return "run-" + s.InvocationID, nil
}
func (f *fakeExecutor) Describe(context.Context, domain.Invocation) (ExecutionState, error) {
	if f.describeResult != (ExecutionState{}) || f.describeErr != nil {
		return f.describeResult, f.describeErr
	}
	return ExecutionState{Status: "running"}, nil
}
func (f *fakeExecutor) Query(_ context.Context, _ domain.Invocation, query string, _ []byte) ([]byte, error) {
	f.lastQuery = query
	if f.queryResult != nil {
		return f.queryResult, nil
	}
	return []byte(`{"status":"running"}`), nil
}
func (f *fakeExecutor) Signal(_ context.Context, _ domain.Invocation, name string, input []byte) error {
	f.signals = append(f.signals, fakeSignal{name: name, input: append([]byte(nil), input...)})
	return f.signalErr
}
func (f *fakeExecutor) Cancel(context.Context, domain.Invocation) error { return nil }

func workerVersionRequest(version string) domain.WorkerVersionRequest {
	return domain.WorkerVersionRequest{
		WorkerName: "payments-worker", Description: "Payment worker version " + version + ".", Image: "registry.example.com/acme/payments@sha256:" + strings.Repeat("a", 64), Version: version,
		Metadata: domain.WorkerMetadata{
			Workflows:  []domain.WorkflowContract{{Name: "ChargeOrder", VersioningBehavior: "pinned", ProjectionQuery: "org_projection", Steps: []domain.DAGStep{{ID: "charge", Label: "Charge"}}}},
			Activities: []domain.ActivityContract{{Name: "ChargeCard", Kind: "write", IdempotencyKey: &domain.IdempotencyKeyContract{Field: "request_id", Derivation: "workflow_id/activity_id"}, RetryPolicy: domain.RetryPolicy{MaximumAttempts: 3}}},
		},
		Runtime: domain.RuntimeSpec{CPU: "250m", Memory: "256Mi", ServiceAccount: "payments-worker"},
		Source:  domain.SourceProvenance{Repository: "https://example.com/acme/payments", Commit: strings.Repeat("b", 40), CIReference: "build-42"},
	}
}

func dynamicServiceDefinition() orgsdk.Definition {
	return orgsdk.Definition{
		Name: "dynamic-workflow",
		Templates: []orgsdk.NodeTemplate{{
			ID: "route", Label: "Choose route", Type: orgsdk.NodeTypeActivity,
			Activity: &orgsdk.ActivityPolicy{SideEffect: orgsdk.SideEffectRead, Retry: orgsdk.RetryPolicy{MaximumAttempts: 3, StartToCloseTimeout: time.Minute}},
		}},
		Bounds: orgsdk.RuntimeBounds{MaxInstancesPerFanOut: 10, MaxRuntimeNodes: 100, MaxProjectionBytes: 64 * 1024},
	}
}

func dynamicServiceWorkerVersionRequest(t *testing.T, definition orgsdk.Definition) domain.WorkerVersionRequest {
	t.Helper()
	manifest, digest, err := orgsdk.GenerateManifest("DynamicWorkflow", definition)
	if err != nil {
		t.Fatal(err)
	}
	request := workerVersionRequest("v1")
	request.ManifestDigest = digest
	request.Metadata = domain.WorkerMetadata{}
	if err := json.Unmarshal(manifest, &request.Metadata); err != nil {
		t.Fatal(err)
	}
	return request
}
