package kube

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBootstrapEvidenceUsesTokenReviewAndRuntimePodImageID(t *testing.T) {
	runner := &sequenceRunner{outputs: [][]byte{
		[]byte(`{"status":{"authenticated":true,"audiences":["org-worker-bootstrap"],"user":{"username":"system:serviceaccount:org-workers:org-acme-worker"}}}`),
		[]byte(`{"items":[{"metadata":{"uid":"pod-1"},"spec":{"serviceAccountName":"org-acme-worker","nodeName":"org-control-plane","containers":[{"name":"worker","image":"registry.example.com/acme/worker@sha256:` + strings.Repeat("a", 64) + `"}]},"status":{"containerStatuses":[{"name":"worker","imageID":"docker-pullable://registry.example.com/acme/worker@sha256:` + strings.Repeat("a", 64) + `"}]}}]}`),
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

func TestKindBootstrapEvidenceVerifiesContainerdImportLink(t *testing.T) {
	expected := "org.local/worker@sha256:" + strings.Repeat("a", 64)
	runtimeID := "docker.io/library/import-2026@sha256:" + strings.Repeat("b", 64)
	runner := &sequenceRunner{outputs: [][]byte{
		[]byte(`{"status":{"authenticated":true,"audiences":["org-worker-bootstrap"],"user":{"username":"system:serviceaccount:org-workers:org-worker"}}}`),
		[]byte(`{"items":[{"metadata":{"uid":"pod-1"},"spec":{"serviceAccountName":"org-worker","nodeName":"org-control-plane","containers":[{"name":"worker","image":"` + expected + `"}]},"status":{"containerStatuses":[{"name":"worker","imageID":"` + runtimeID + `"}]}}]}`),
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
