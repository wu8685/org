package kube

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBootstrapEvidenceUsesTokenReviewAndRuntimePodImageID(t *testing.T) {
	runner := &sequenceRunner{outputs: [][]byte{
		[]byte(`{"status":{"authenticated":true,"audiences":["org-worker-bootstrap"],"user":{"username":"system:serviceaccount:org-workers:org-acme-worker","extra":{"authentication.kubernetes.io/pod-uid":["pod-1"]}}}}`),
		[]byte(`{"items":[{"metadata":{"uid":"pod-1","labels":{"org.wu8685.dev/tenant-hash":"tenant-hash","org.wu8685.dev/worker":"worker","org.wu8685.dev/version":"version-hash","org.wu8685.dev/bootstrap-generation":"generation-1"},"ownerReferences":[{"apiVersion":"apps/v1","kind":"ReplicaSet","name":"worker-rs","uid":"rs-1","controller":true}]},"spec":{"serviceAccountName":"org-acme-worker","nodeName":"org-control-plane","containers":[{"name":"worker","image":"registry.example.com/acme/worker@sha256:` + strings.Repeat("a", 64) + `"}]},"status":{"containerStatuses":[{"name":"worker","imageID":"docker-pullable://registry.example.com/acme/worker@sha256:` + strings.Repeat("a", 64) + `"}]}}]}`),
		[]byte(`{"metadata":{"uid":"rs-1","ownerReferences":[{"apiVersion":"apps/v1","kind":"Deployment","name":"worker-deployment","uid":"deployment-1","controller":true}]}}`),
	}}
	resolver := NewBootstrapEvidenceResolver(Config{Namespace: "org-workers", Context: "kind-org"}, runner)
	request := httptest.NewRequest("POST", "/internal/v1/bootstrap/register", nil)
	request.Header.Set("X-Org-Workload-Token", "bound-token")
	request.Header.Set("X-Org-Pod-UID", "pod-1")
	evidence, err := resolver.ResolveBootstrapEvidence(request)
	if err != nil {
		t.Fatal(err)
	}
	if !evidence.AudienceVerified || evidence.PodUID != "pod-1" || evidence.ObservedImage != "registry.example.com/acme/worker@sha256:"+strings.Repeat("a", 64) {
		t.Fatalf("evidence = %#v", evidence)
	}
	joined := strings.Join(runner.calls, "\n")
	if !strings.Contains(joined, "create --raw /apis/authentication.k8s.io/v1/tokenreviews") || !strings.Contains(joined, "--context kind-org") {
		t.Fatalf("kubectl calls = %s", joined)
	}
}

func TestBootstrapEvidenceRejectsWrongAudienceOrServiceAccount(t *testing.T) {
	for _, tokenReview := range []string{
		`{"status":{"authenticated":true,"audiences":["other"],"user":{"username":"system:serviceaccount:org-workers:org-acme-worker"}}}`,
		`{"status":{"authenticated":true,"audiences":["org-worker-bootstrap"],"user":{"username":"system:serviceaccount:other:org-acme-worker"}}}`,
	} {
		runner := &sequenceRunner{outputs: [][]byte{[]byte(tokenReview)}}
		resolver := NewBootstrapEvidenceResolver(Config{Namespace: "org-workers"}, runner)
		request := httptest.NewRequest("POST", "/", nil)
		request.Header.Set("X-Org-Workload-Token", "bound-token")
		request.Header.Set("X-Org-Pod-UID", "pod-1")
		if _, err := resolver.ResolveBootstrapEvidence(request); err == nil {
			t.Fatalf("TokenReview accepted: %s", tokenReview)
		}
	}
}

func TestBootstrapEvidenceRejectsMissingOrMismatchedTokenPodUIDClaim(t *testing.T) {
	image := "registry.example.com/acme/worker@sha256:" + strings.Repeat("a", 64)
	pods := `{"items":[{"metadata":{"uid":"pod-1"},"spec":{"serviceAccountName":"org-acme-worker","nodeName":"org-control-plane","containers":[{"name":"worker","image":"` + image + `"}]},"status":{"containerStatuses":[{"name":"worker","imageID":"docker-pullable://` + image + `"}]}}]}`
	for name, extra := range map[string]string{
		"missing":    `{}`,
		"mismatched": `{"authentication.kubernetes.io/pod-uid":["stale-pod"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			runner := &sequenceRunner{outputs: [][]byte{
				[]byte(`{"status":{"authenticated":true,"audiences":["org-worker-bootstrap"],"user":{"username":"system:serviceaccount:org-workers:org-acme-worker","extra":` + extra + `}}}`),
				[]byte(pods),
			}}
			resolver := NewBootstrapEvidenceResolver(Config{Namespace: "org-workers"}, runner)
			request := httptest.NewRequest("POST", "/", nil)
			request.Header.Set("X-Org-Workload-Token", "bound-token")
			request.Header.Set("X-Org-Pod-UID", "pod-1")
			if _, err := resolver.ResolveBootstrapEvidence(request); err == nil {
				t.Fatal("TokenReview without the exact bound Pod UID claim was accepted")
			}
		})
	}
}

func TestKindBootstrapEvidenceVerifiesContainerdImportLink(t *testing.T) {
	expected := "org.local/worker@sha256:" + strings.Repeat("a", 64)
	runtimeID := "docker.io/library/import-2026@sha256:" + strings.Repeat("b", 64)
	runner := &sequenceRunner{outputs: [][]byte{
		[]byte(`{"status":{"authenticated":true,"audiences":["org-worker-bootstrap"],"user":{"username":"system:serviceaccount:org-workers:org-worker","extra":{"authentication.kubernetes.io/pod-uid":["pod-1"]}}}}`),
		[]byte(`{"items":[{"metadata":{"uid":"pod-1","labels":{"org.wu8685.dev/tenant-hash":"tenant-hash","org.wu8685.dev/worker":"worker","org.wu8685.dev/version":"version-hash","org.wu8685.dev/bootstrap-generation":"generation-1"},"ownerReferences":[{"apiVersion":"apps/v1","kind":"ReplicaSet","name":"worker-rs","uid":"rs-1","controller":true}]},"spec":{"serviceAccountName":"org-worker","nodeName":"org-control-plane","containers":[{"name":"worker","image":"` + expected + `"}]},"status":{"containerStatuses":[{"name":"worker","imageID":"` + runtimeID + `"}]}}]}`),
		[]byte(`{"metadata":{"uid":"rs-1","ownerReferences":[{"apiVersion":"apps/v1","kind":"Deployment","name":"worker-deployment","uid":"deployment-1","controller":true}]}}`),
		[]byte(`{"status":{"repoDigests":["` + expected + `","` + runtimeID + `"]}}`),
	}}
	resolver := NewBootstrapEvidenceResolver(Config{Namespace: "org-workers", Context: "kind-org"}, runner)
	request := httptest.NewRequest("POST", "/", nil)
	request.Header.Set("X-Org-Workload-Token", "token")
	request.Header.Set("X-Org-Pod-UID", "pod-1")
	evidence, err := resolver.ResolveBootstrapEvidence(request)
	if err != nil || evidence.ObservedImage != expected || evidence.RuntimeImageID != runtimeID || !evidence.RuntimeLinkVerified {
		t.Fatalf("evidence=%#v error=%v", evidence, err)
	}
}

func TestBootstrapEvidenceResolvesCandidateLabelsAndDeploymentOwner(t *testing.T) {
	expected := "org.local/worker@sha256:" + strings.Repeat("a", 64)
	runner := &sequenceRunner{outputs: [][]byte{
		[]byte(`{"status":{"authenticated":true,"audiences":["org-worker-bootstrap"],"user":{"username":"system:serviceaccount:org-workers:org-worker","extra":{"authentication.kubernetes.io/pod-uid":["pod-1"]}}}}`),
		[]byte(`{"items":[{"metadata":{"uid":"pod-1","labels":{"org.wu8685.dev/tenant-hash":"tenant-hash","org.wu8685.dev/worker":"worker","org.wu8685.dev/version":"version-hash","org.wu8685.dev/bootstrap-generation":"generation-1"},"ownerReferences":[{"apiVersion":"apps/v1","kind":"ReplicaSet","name":"org-worker-version-rs","uid":"rs-1","controller":true}]},"spec":{"serviceAccountName":"org-worker","nodeName":"org-control-plane","containers":[{"name":"worker","image":"` + expected + `"}]},"status":{"containerStatuses":[{"name":"worker","imageID":"` + expected + `"}]}}]}`),
		[]byte(`{"metadata":{"uid":"rs-1","ownerReferences":[{"apiVersion":"apps/v1","kind":"Deployment","name":"org-worker-version","uid":"deployment-1","controller":true}]}}`),
	}}
	resolver := NewBootstrapEvidenceResolver(Config{Namespace: "org-workers", Context: "kind-org"}, runner)
	request := httptest.NewRequest("POST", "/", nil)
	request.Header.Set("X-Org-Workload-Token", "token")
	request.Header.Set("X-Org-Pod-UID", "pod-1")
	evidence, err := resolver.ResolveBootstrapEvidence(request)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.TenantHash != "tenant-hash" || evidence.WorkerName != "worker" || evidence.VersionHash != "version-hash" || evidence.DeploymentGeneration != "generation-1" || evidence.OwnerDeployment != "org-worker-version" {
		t.Fatalf("candidate identity evidence = %#v", evidence)
	}
}

func TestBootstrapEvidenceRejectsMissingLabelsOrBrokenReplicaSetOwner(t *testing.T) {
	expected := "org.local/worker@sha256:" + strings.Repeat("a", 64)
	tokenReview := []byte(`{"status":{"authenticated":true,"audiences":["org-worker-bootstrap"],"user":{"username":"system:serviceaccount:org-workers:org-worker","extra":{"authentication.kubernetes.io/pod-uid":["pod-1"]}}}}`)
	validLabels := `"labels":{"org.wu8685.dev/tenant-hash":"tenant-hash","org.wu8685.dev/worker":"worker","org.wu8685.dev/version":"version-hash","org.wu8685.dev/bootstrap-generation":"generation-1"},`
	for name, outputs := range map[string][][]byte{
		"missing-labels": {
			tokenReview,
			[]byte(`{"items":[{"metadata":{"uid":"pod-1","ownerReferences":[{"apiVersion":"apps/v1","kind":"ReplicaSet","name":"worker-rs","uid":"rs-1","controller":true}]},"spec":{"serviceAccountName":"org-worker","containers":[{"name":"worker","image":"` + expected + `"}]},"status":{"containerStatuses":[{"name":"worker","imageID":"` + expected + `"}]}}]}`),
		},
		"replicaset-uid": {
			tokenReview,
			[]byte(`{"items":[{"metadata":{"uid":"pod-1",` + validLabels + `"ownerReferences":[{"apiVersion":"apps/v1","kind":"ReplicaSet","name":"worker-rs","uid":"rs-stale","controller":true}]},"spec":{"serviceAccountName":"org-worker","containers":[{"name":"worker","image":"` + expected + `"}]},"status":{"containerStatuses":[{"name":"worker","imageID":"` + expected + `"}]}}]}`),
			[]byte(`{"metadata":{"uid":"rs-live","ownerReferences":[{"apiVersion":"apps/v1","kind":"Deployment","name":"worker-deployment","uid":"deployment-1","controller":true}]}}`),
		},
	} {
		t.Run(name, func(t *testing.T) {
			resolver := NewBootstrapEvidenceResolver(Config{Namespace: "org-workers"}, &sequenceRunner{outputs: outputs})
			request := httptest.NewRequest("POST", "/", nil)
			request.Header.Set("X-Org-Workload-Token", "token")
			request.Header.Set("X-Org-Pod-UID", "pod-1")
			if _, err := resolver.ResolveBootstrapEvidence(request); err == nil {
				t.Fatal("invalid candidate Pod owner/labels were accepted")
			}
		})
	}
}

type sequenceRunner struct {
	outputs [][]byte
	calls   []string
}

func (r *sequenceRunner) Run(_ context.Context, stdin, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, name+" "+strings.Join(args, " ")+" stdin="+stdin)
	output := r.outputs[0]
	r.outputs = r.outputs[1:]
	return output, nil
}
