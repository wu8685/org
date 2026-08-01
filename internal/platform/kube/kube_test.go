package kube

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wu8685/org/internal/domain"
	"github.com/wu8685/org/internal/service"
)

func TestBootstrapManifestInjectsCredentialAsReadonlyFileAndNoPublicIdentity(t *testing.T) {
	d := testDeployment()
	manifest, err := RenderBootstrapManifest(d, Config{Namespace: "org-workers", WorkerTemporalAddress: "host.docker.internal:7233", TemporalNamespace: "default"}, service.BootstrapDeployment{Endpoint: "https://host.docker.internal:8090/internal/v1/bootstrap/register", Credential: "opaque-secret", Generation: "generation-random", ExpiresAt: time.Now().Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"kind: Secret", "opaque-secret", "ORG_BOOTSTRAP_ENDPOINT", "/var/run/org-bootstrap/credential", "readOnly: true", "audience: org-worker-bootstrap", "ORG_BOOTSTRAP_WORKLOAD_TOKEN_FILE", "ORG_BOOTSTRAP_POD_UID", "fieldPath: metadata.uid", "fsGroup: 65532", "defaultMode: 0440", "org.wu8685.dev/bootstrap-generation: generation-random"} {
		if !strings.Contains(manifest, want) {
			t.Fatalf("bootstrap manifest missing %q\n%s", want, manifest)
		}
	}
	if strings.Contains(manifest, "ORG_TENANT") || strings.Contains(manifest, "ORG_WORKER_NAME") || strings.Contains(manifest, "ORG_WORKER_VERSION") {
		t.Fatalf("bootstrap target identity was injected into Worker configuration\n%s", manifest)
	}
}

func TestManifestIsDigestPinnedConstrainedAndUsesPodReachableTemporalAddress(t *testing.T) {
	d := testDeployment()
	manifest, err := RenderManifest(d, Config{Namespace: "org-workers", WorkerTemporalAddress: "host.docker.internal:7233", TemporalNamespace: "default"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"kind: ServiceAccount", "kind: Deployment", "runAsNonRoot: true", "allowPrivilegeEscalation: false", "readOnlyRootFilesystem: true", "automountServiceAccountToken: false", "host.docker.internal:7233", d.TaskQueue, d.Image} {
		if !strings.Contains(manifest, want) {
			t.Errorf("manifest missing %q\n%s", want, manifest)
		}
	}
	if strings.Contains(manifest, "value: 127.0.0.1:7233") {
		t.Fatal("worker manifest hard-codes host localhost")
	}
}

func TestKubectlUsesConfiguredContextForApplyAndReadiness(t *testing.T) {
	runner := &fakeRunner{}
	client := New(Config{Namespace: "org-workers", Context: "kind-org", Kubeconfig: "/tmp/test-kubeconfig", WorkerTemporalAddress: "host.docker.internal:7233", TemporalNamespace: "default"}, runner)
	d := testDeployment()
	if err := client.Apply(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if err := client.WaitReady(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(runner.calls, "\n")
	for _, want := range []string{"--context kind-org", "--kubeconfig /tmp/test-kubeconfig", "apply -f -", "rollout status deployment/"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("kubectl calls missing %q: %s", want, joined)
		}
	}
}

func TestApplyCreatesMissingPlatformNamespaceBeforeNamespaceScopedResources(t *testing.T) {
	runner := &scriptedRunner{results: []runnerResult{{err: errors.New(`Error from server (NotFound): namespaces "org-workers" not found`)}, {}, {}}}
	client := New(Config{Namespace: "org-workers", Context: "kind-org", WorkerTemporalAddress: "host.docker.internal:7233", TemporalNamespace: "default"}, runner)
	if err := client.ApplyBootstrap(context.Background(), testDeployment(), service.BootstrapDeployment{Endpoint: "http://host.docker.internal:8090/internal/v1/bootstrap/register", Credential: "secret", Generation: "generation-1", ExpiresAt: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 3 {
		t.Fatalf("calls=%v", runner.calls)
	}
	for index, want := range []string{"get namespace org-workers", "create -f -", "apply -f -"} {
		if !strings.Contains(runner.calls[index], want) {
			t.Fatalf("call %d missing %q: %s", index, want, runner.calls[index])
		}
	}
	if !strings.Contains(runner.calls[1], "kind: Namespace") || !strings.Contains(runner.calls[1], "app.kubernetes.io/managed-by: org") {
		t.Fatalf("created Namespace is not marked as org-created: %s", runner.calls[1])
	}
	if strings.Contains(runner.calls[2], "kind: Namespace") {
		t.Fatalf("workload apply attempted to own Namespace: %s", runner.calls[2])
	}
}

func TestApplyPreservesExistingPlatformNamespace(t *testing.T) {
	runner := &scriptedRunner{results: []runnerResult{{}, {}}}
	client := New(Config{Namespace: "shared-existing", Context: "kind-org", WorkerTemporalAddress: "host.docker.internal:7233", TemporalNamespace: "default"}, runner)
	if err := client.Apply(context.Background(), testDeployment()); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 2 || !strings.Contains(runner.calls[0], "get namespace shared-existing") || !strings.Contains(runner.calls[1], "apply -f -") {
		t.Fatalf("existing Namespace call sequence=%v", runner.calls)
	}
	if strings.Contains(runner.calls[1], "kind: Namespace") || strings.Contains(strings.Join(runner.calls, "\n"), "create -f -") {
		t.Fatalf("existing Namespace was mutated or adopted: %v", runner.calls)
	}
}

func TestApplyDoesNotCreateNamespaceAfterNonNotFoundReadFailure(t *testing.T) {
	runner := &scriptedRunner{results: []runnerResult{{err: errors.New("forbidden")}}}
	client := New(Config{Namespace: "org-workers", Context: "kind-org", WorkerTemporalAddress: "host.docker.internal:7233", TemporalNamespace: "default"}, runner)
	if err := client.Apply(context.Background(), testDeployment()); err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("error=%v", err)
	}
	if len(runner.calls) != 1 || strings.Contains(runner.calls[0], "create") || strings.Contains(runner.calls[0], "apply") {
		t.Fatalf("unsafe calls after namespace read failure: %v", runner.calls)
	}
}

func TestApplyConvergesWhenNamespaceIsCreatedConcurrently(t *testing.T) {
	runner := &scriptedRunner{results: []runnerResult{
		{err: errors.New(`Error from server (NotFound): namespaces "org-workers" not found`)},
		{err: errors.New(`Error from server (AlreadyExists): namespaces "org-workers" already exists`)},
		{}, {},
	}}
	client := New(Config{Namespace: "org-workers", Context: "kind-org", WorkerTemporalAddress: "host.docker.internal:7233", TemporalNamespace: "default"}, runner)
	if err := client.Apply(context.Background(), testDeployment()); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 4 || !strings.Contains(runner.calls[2], "get namespace org-workers") || !strings.Contains(runner.calls[3], "apply -f -") {
		t.Fatalf("concurrent Namespace creation did not converge: %v", runner.calls)
	}
}

func TestApplyDoesNotTreatAnUnrelatedNotFoundAsNamespaceAbsence(t *testing.T) {
	runner := &scriptedRunner{results: []runnerResult{{err: errors.New(`Error from server (NotFound): contexts "kind-org" not found`)}}}
	client := New(Config{Namespace: "org-workers", Context: "kind-org", WorkerTemporalAddress: "host.docker.internal:7233", TemporalNamespace: "default"}, runner)
	if err := client.Apply(context.Background(), testDeployment()); err == nil {
		t.Fatal("unrelated NotFound was treated as a missing platform Kubernetes Namespace")
	}
	if len(runner.calls) != 1 {
		t.Fatalf("unexpected mutation after unrelated NotFound: %v", runner.calls)
	}
}

func TestManifestUsesTenantCanonicalNamesLabelsAndNoKubernetesAPIAccess(t *testing.T) {
	d := testDeployment()
	d.Runtime.ServiceAccount = "forged-client-service-account"
	manifest, err := RenderManifest(d, Config{Namespace: "org-platform", WorkerTemporalAddress: "temporal.org.local:7233", TemporalNamespace: "platform"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"name: " + d.KubernetesDeployment,
		"serviceAccountName: " + d.KubernetesServiceAccount,
		"org.wu8685.dev/tenant: " + d.TenantSlug,
		"org.wu8685.dev/tenant-hash: " + d.TenantHash,
		"org.wu8685.dev/worker: " + d.WorkerName,
		"org.wu8685.dev/version: " + d.VersionHash,
		"requests: {cpu: 250m, memory: 256Mi}",
		"limits: {cpu: 250m, memory: 256Mi}",
		"automountServiceAccountToken: false",
	} {
		if !strings.Contains(manifest, want) {
			t.Errorf("manifest missing %q\n%s", want, manifest)
		}
	}
	if strings.Contains(manifest, "forged-client-service-account") || strings.Contains(manifest, "RoleBinding") || strings.Contains(manifest, "kind: Role\n") {
		t.Fatalf("manifest granted or accepted Kubernetes API identity\n%s", manifest)
	}
	if strings.Contains(manifest, "org.wu8685.dev/scope") || strings.Contains(manifest, "org.wu8685.dev/release") {
		t.Fatalf("legacy public labels leaked into manifest\n%s", manifest)
	}
}

func TestOptionalNetworkPolicyReportsManifestOnlyWithoutCNIEnforcement(t *testing.T) {
	d := testDeployment()
	cfg := Config{Namespace: "org-platform", WorkerTemporalAddress: "temporal.org.local:7233", TemporalNamespace: "platform", NetworkPolicyEnabled: true}
	manifest, err := RenderManifest(d, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(manifest, "kind: NetworkPolicy") || NetworkPolicyStatus(cfg) != "manifest_only_not_enforced" {
		t.Fatalf("NetworkPolicy status/manifest mismatch: status=%q\n%s", NetworkPolicyStatus(cfg), manifest)
	}
}

type fakeRunner struct{ calls []string }

func (f *fakeRunner) Run(_ context.Context, stdin string, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " ")+" stdin="+stdin)
	return nil, nil
}

type runnerResult struct {
	out []byte
	err error
}

type scriptedRunner struct {
	calls   []string
	results []runnerResult
}

func (r *scriptedRunner) Run(_ context.Context, stdin string, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, name+" "+strings.Join(args, " ")+" stdin="+stdin)
	if len(r.results) == 0 {
		return nil, errors.New("unexpected runner call")
	}
	result := r.results[0]
	r.results = r.results[1:]
	return result.out, result.err
}

func testDeployment() domain.WorkerVersion {
	return domain.WorkerVersion{ID: "ver-1", TenantID: "tenant-a", TenantSlug: "alpha", TenantHash: "h2m4abc123", VersionHash: "version123", WorkerName: "payments-worker", Version: "v1", Image: "registry.example.com/acme/payments@sha256:" + strings.Repeat("a", 64), TaskQueue: "org-alpha-payments-worker-hash123456", WorkerDeployment: "org-alpha-payments-worker-hash123456", KubernetesDeployment: "org-alpha-payments-worker-hash123456-version123", KubernetesServiceAccount: "org-alpha-payments-worker-hash123456", KubernetesNetworkPolicy: "org-alpha-payments-worker-np-version123", Runtime: domain.RuntimeSpec{CPU: "250m", Memory: "256Mi", ServiceAccount: "org-alpha-payments-worker-hash123456"}}
}
