package kube

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

func TestBootstrapEvidenceUsesTypedTokenReviewAndRuntimeObjects(t *testing.T) {
	expected := "registry.example.com/acme/worker@sha256:" + strings.Repeat("a", 64)
	api := validBootstrapClient(expected, expected)
	resolver := NewBootstrapEvidenceResolver(Config{Namespace: "org-workers"}, api, nil)
	evidence, err := resolver.ResolveBootstrapEvidence(bootstrapRequest("bound-token", "pod-1"))
	if err != nil {
		t.Fatal(err)
	}
	if !evidence.AudienceVerified || evidence.PodUID != "pod-1" || evidence.ObservedImage != expected || evidence.OwnerDeployment != "worker-deployment" {
		t.Fatalf("evidence = %#v", evidence)
	}
	for _, action := range api.Actions() {
		if action.GetResource().Resource == "tokenreviews" && action.GetVerb() == "create" {
			review := action.(ktesting.CreateAction).GetObject().(*authenticationv1.TokenReview)
			if len(review.Spec.Audiences) != 1 || review.Spec.Audiences[0] != bootstrapAudience || review.Spec.Token != "bound-token" {
				t.Fatalf("TokenReview request = %#v", review.Spec)
			}
			return
		}
	}
	t.Fatal("typed TokenReview API was not called")
}

func TestBootstrapEvidenceRejectsWrongAudienceServiceAccountOrPodClaim(t *testing.T) {
	expected := "registry.example.com/acme/worker@sha256:" + strings.Repeat("a", 64)
	for name, mutate := range map[string]func(*authenticationv1.TokenReview){
		"audience": func(review *authenticationv1.TokenReview) { review.Status.Audiences = []string{"other"} },
		"namespace": func(review *authenticationv1.TokenReview) {
			review.Status.User.Username = "system:serviceaccount:other:org-worker"
		},
		"pod-uid": func(review *authenticationv1.TokenReview) {
			review.Status.User.Extra[tokenReviewPodUIDClaim] = authenticationv1.ExtraValue{"stale-pod"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			api := validBootstrapClient(expected, expected)
			api.PrependReactor("create", "tokenreviews", func(action ktesting.Action) (bool, runtime.Object, error) {
				review := validTokenReview()
				mutate(review)
				return true, review, nil
			})
			resolver := NewBootstrapEvidenceResolver(Config{Namespace: "org-workers"}, api, nil)
			if _, err := resolver.ResolveBootstrapEvidence(bootstrapRequest("token", "pod-1")); err == nil {
				t.Fatal("invalid TokenReview was accepted")
			}
		})
	}
}

func TestBootstrapEvidenceRejectsMissingLabelsAndBrokenOwnerUID(t *testing.T) {
	expected := "registry.example.com/acme/worker@sha256:" + strings.Repeat("a", 64)
	for name, mutate := range map[string]func(*corev1.Pod, *appsv1.ReplicaSet){
		"labels":           func(pod *corev1.Pod, _ *appsv1.ReplicaSet) { pod.Labels = nil },
		"owner-uid":        func(_ *corev1.Pod, replicaSet *appsv1.ReplicaSet) { replicaSet.UID = "other-rs" },
		"deployment-owner": func(_ *corev1.Pod, replicaSet *appsv1.ReplicaSet) { replicaSet.OwnerReferences = nil },
	} {
		t.Run(name, func(t *testing.T) {
			pod, replicaSet := validPodAndReplicaSet(expected, expected)
			mutate(pod, replicaSet)
			api := bootstrapClient(pod, replicaSet)
			resolver := NewBootstrapEvidenceResolver(Config{Namespace: "org-workers"}, api, nil)
			if _, err := resolver.ResolveBootstrapEvidence(bootstrapRequest("token", "pod-1")); err == nil {
				t.Fatal("invalid candidate identity was accepted")
			}
		})
	}
}

func TestKindBootstrapEvidenceUsesIsolatedRuntimeImageVerifier(t *testing.T) {
	expected := "org.local/worker@sha256:" + strings.Repeat("a", 64)
	runtimeID := "docker.io/library/import-2026@sha256:" + strings.Repeat("b", 64)
	api := validBootstrapClient(expected, runtimeID)
	verifier := &fakeRuntimeImageVerifier{}
	resolver := NewBootstrapEvidenceResolver(Config{Namespace: "org-workers", Context: "kind-org"}, api, verifier)
	evidence, err := resolver.ResolveBootstrapEvidence(bootstrapRequest("token", "pod-1"))
	if err != nil {
		t.Fatal(err)
	}
	if verifier.node != "org-control-plane" || verifier.declared != expected || verifier.runtime != runtimeID || !evidence.RuntimeLinkVerified {
		t.Fatalf("verifier=%#v evidence=%#v", verifier, evidence)
	}
}

func TestNonKindBootstrapRejectsRuntimeImageMismatchWithoutVerifier(t *testing.T) {
	expected := "registry.example.com/worker@sha256:" + strings.Repeat("a", 64)
	runtimeID := "registry.example.com/worker@sha256:" + strings.Repeat("b", 64)
	verifier := &fakeRuntimeImageVerifier{}
	resolver := NewBootstrapEvidenceResolver(Config{Namespace: "org-workers", Context: "production"}, validBootstrapClient(expected, runtimeID), verifier)
	if _, err := resolver.ResolveBootstrapEvidence(bootstrapRequest("token", "pod-1")); err == nil {
		t.Fatal("production runtime image mismatch was accepted")
	}
	if verifier.node != "" {
		t.Fatal("development verifier was called outside kind")
	}
}

func TestKindBootstrapPropagatesRuntimeVerifierFailure(t *testing.T) {
	expected := "org.local/worker@sha256:" + strings.Repeat("a", 64)
	runtimeID := "docker.io/library/import@sha256:" + strings.Repeat("b", 64)
	verifier := &fakeRuntimeImageVerifier{err: errors.New("not linked")}
	resolver := NewBootstrapEvidenceResolver(Config{Namespace: "org-workers", Context: "kind-org"}, validBootstrapClient(expected, runtimeID), verifier)
	if _, err := resolver.ResolveBootstrapEvidence(bootstrapRequest("token", "pod-1")); err == nil || !strings.Contains(err.Error(), "not linked") {
		t.Fatalf("error = %v", err)
	}
}

type fakeRuntimeImageVerifier struct {
	node, declared, runtime string
	err                     error
}

func (f *fakeRuntimeImageVerifier) Verify(_ context.Context, node, declared, runtime string) error {
	f.node, f.declared, f.runtime = node, declared, runtime
	return f.err
}

func bootstrapRequest(token, podUID string) *http.Request {
	request := httptest.NewRequest("POST", "/internal/v1/bootstrap/register", nil)
	request.Header.Set("X-Org-Workload-Token", token)
	request.Header.Set("X-Org-Pod-UID", podUID)
	return request
}

func validBootstrapClient(expected, runtimeID string) *fake.Clientset {
	pod, replicaSet := validPodAndReplicaSet(expected, runtimeID)
	return bootstrapClient(pod, replicaSet)
}

func bootstrapClient(pod *corev1.Pod, replicaSet *appsv1.ReplicaSet) *fake.Clientset {
	api := fake.NewClientset(pod, replicaSet)
	api.PrependReactor("create", "tokenreviews", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, validTokenReview(), nil
	})
	return api
}

func validTokenReview() *authenticationv1.TokenReview {
	return &authenticationv1.TokenReview{Status: authenticationv1.TokenReviewStatus{
		Authenticated: true,
		Audiences:     []string{bootstrapAudience},
		User: authenticationv1.UserInfo{
			Username: "system:serviceaccount:org-workers:org-worker",
			Extra:    map[string]authenticationv1.ExtraValue{tokenReviewPodUIDClaim: {"pod-1"}},
		},
	}}
}

func validPodAndReplicaSet(expected, runtimeID string) (*corev1.Pod, *appsv1.ReplicaSet) {
	controller := true
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "worker-pod",
			Namespace: "org-workers",
			UID:       types.UID("pod-1"),
			Labels: map[string]string{
				tenantHashLabel: "tenant-hash", workerLabel: "worker", versionHashLabel: "version-hash", bootstrapGenerationLabel: "generation-1", managedByLabel: managedByValue,
			},
			OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "worker-rs", UID: "rs-1", Controller: &controller}},
		},
		Spec: corev1.PodSpec{
			ServiceAccountName: "org-worker",
			NodeName:           "org-control-plane",
			Containers:         []corev1.Container{{Name: "worker", Image: expected}},
		},
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{Name: "worker", ImageID: runtimeID}}},
	}
	replicaSet := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{
		Name: "worker-rs", Namespace: "org-workers", UID: "rs-1",
		OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "Deployment", Name: "worker-deployment", UID: "deployment-1", Controller: &controller}},
	}}
	return pod, replicaSet
}
