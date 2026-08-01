package kube

import (
	"context"
	"strings"
	"testing"

	"github.com/wu8685/org/internal/domain"
)

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

func testDeployment() domain.WorkerVersion {
	return domain.WorkerVersion{ID: "ver-1", TenantID: "tenant-a", TenantSlug: "alpha", TenantHash: "h2m4abc123", VersionHash: "version123", WorkerName: "payments-worker", Version: "v1", Image: "registry.example.com/acme/payments@sha256:" + strings.Repeat("a", 64), TaskQueue: "org-alpha-payments-worker-hash123456", WorkerDeployment: "org-alpha-payments-worker-hash123456", KubernetesDeployment: "org-alpha-payments-worker-hash123456-version123", KubernetesServiceAccount: "org-alpha-payments-worker-hash123456", KubernetesNetworkPolicy: "org-alpha-payments-worker-np-version123", Runtime: domain.RuntimeSpec{CPU: "250m", Memory: "256Mi", ServiceAccount: "org-alpha-payments-worker-hash123456"}}
}
