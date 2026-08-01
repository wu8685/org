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
	if current.SelectedVersion != "v2" || executor.starts[0].PinnedVersion != "" {
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

type fakeCluster struct {
	calls     []string
	bootstrap BootstrapDeployment
	waitReady func(domain.WorkerVersion) error
}

func (f *fakeCluster) Apply(context.Context, domain.WorkerVersion) error {
	f.calls = append(f.calls, "apply")
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
	calls       []string
	starts      []ExecutionStart
	queryResult []byte
	lastQuery   string
	signals     []fakeSignal
	signalErr   error
	probeResult RuntimeIdentity
	probeErr    error
}

type fakeSignal struct {
	name  string
	input []byte
}

func (f *fakeExecutor) WaitForPoller(context.Context, domain.WorkerVersion) error {
	f.calls = append(f.calls, "wait-temporal")
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
func (f *fakeExecutor) SetCurrent(context.Context, domain.WorkerVersion) error {
	f.calls = append(f.calls, "set-current")
	return nil
}
func (f *fakeExecutor) Start(_ context.Context, s ExecutionStart) (string, error) {
	f.starts = append(f.starts, s)
	return "run-" + s.InvocationID, nil
}
func (f *fakeExecutor) Describe(context.Context, domain.Invocation) (ExecutionState, error) {
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
