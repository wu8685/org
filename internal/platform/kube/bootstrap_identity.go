package kube

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/wu8685/org/internal/service"
)

const bootstrapAudience = "org-worker-bootstrap"
const tokenReviewPodUIDClaim = "authentication.kubernetes.io/pod-uid"

const (
	tenantHashLabel          = "org.wu8685.dev/tenant-hash"
	workerLabel              = "org.wu8685.dev/worker"
	versionHashLabel         = "org.wu8685.dev/version"
	bootstrapGenerationLabel = "org.wu8685.dev/bootstrap-generation"
)

type RuntimeImageLinkVerifier interface {
	Verify(context.Context, string, string, string) error
}

type BootstrapEvidenceResolver struct {
	cfg           Config
	api           kubernetes.Interface
	imageVerifier RuntimeImageLinkVerifier
}

func NewBootstrapEvidenceResolver(cfg Config, api kubernetes.Interface, imageVerifier RuntimeImageLinkVerifier) *BootstrapEvidenceResolver {
	if imageVerifier == nil {
		imageVerifier = NewKindRuntimeImageVerifier(nil)
	}
	return &BootstrapEvidenceResolver{cfg: cfg, api: api, imageVerifier: imageVerifier}
}

func (r *BootstrapEvidenceResolver) ResolveBootstrapEvidence(request *http.Request) (service.BootstrapWorkloadEvidence, error) {
	token := strings.TrimSpace(request.Header.Get("X-Org-Workload-Token"))
	podUID := strings.TrimSpace(request.Header.Get("X-Org-Pod-UID"))
	if token == "" || podUID == "" || r.cfg.Namespace == "" {
		return service.BootstrapWorkloadEvidence{}, errors.New("workload token, Pod UID, and platform Kubernetes Namespace are required")
	}
	if r.api == nil {
		return service.BootstrapWorkloadEvidence{}, errors.New("Kubernetes API client is required")
	}

	review, err := r.api.AuthenticationV1().TokenReviews().Create(request.Context(), &authenticationv1.TokenReview{
		TypeMeta: metav1.TypeMeta{APIVersion: "authentication.k8s.io/v1", Kind: "TokenReview"},
		Spec:     authenticationv1.TokenReviewSpec{Token: token, Audiences: []string{bootstrapAudience}},
	}, metav1.CreateOptions{})
	if err != nil {
		return service.BootstrapWorkloadEvidence{}, fmt.Errorf("review workload token: %w", err)
	}
	wantPrefix := "system:serviceaccount:" + r.cfg.Namespace + ":"
	if !review.Status.Authenticated || !containsString(review.Status.Audiences, bootstrapAudience) || !strings.HasPrefix(review.Status.User.Username, wantPrefix) {
		return service.BootstrapWorkloadEvidence{}, errors.New("TokenReview did not authenticate the required audience and platform ServiceAccount")
	}
	claimedPodUIDs := review.Status.User.Extra[tokenReviewPodUIDClaim]
	if len(claimedPodUIDs) != 1 || claimedPodUIDs[0] != podUID {
		return service.BootstrapWorkloadEvidence{}, errors.New("TokenReview Pod UID claim does not match the requesting Pod")
	}
	serviceAccount := strings.TrimPrefix(review.Status.User.Username, wantPrefix)

	pods, err := r.api.CoreV1().Pods(r.cfg.Namespace).List(request.Context(), metav1.ListOptions{LabelSelector: managedByLabel + "=" + managedByValue})
	if err != nil {
		return service.BootstrapWorkloadEvidence{}, fmt.Errorf("list candidate Pods: %w", err)
	}
	for _, pod := range pods.Items {
		if string(pod.UID) != podUID {
			continue
		}
		if pod.Spec.ServiceAccountName != serviceAccount {
			return service.BootstrapWorkloadEvidence{}, errors.New("TokenReview ServiceAccount does not own the Pod")
		}
		tenantHash := pod.Labels[tenantHashLabel]
		workerName := pod.Labels[workerLabel]
		versionHash := pod.Labels[versionHashLabel]
		generation := pod.Labels[bootstrapGenerationLabel]
		if tenantHash == "" || workerName == "" || versionHash == "" || generation == "" {
			return service.BootstrapWorkloadEvidence{}, errors.New("candidate Pod is missing server-owned bootstrap identity labels")
		}
		replicaSetOwner, ok := controllerOwner(pod.OwnerReferences, "ReplicaSet")
		if !ok {
			return service.BootstrapWorkloadEvidence{}, errors.New("candidate Pod is not owned by a ReplicaSet controller")
		}
		replicaSet, err := r.api.AppsV1().ReplicaSets(r.cfg.Namespace).Get(request.Context(), replicaSetOwner.Name, metav1.GetOptions{})
		if err != nil {
			return service.BootstrapWorkloadEvidence{}, fmt.Errorf("read candidate ReplicaSet: %w", err)
		}
		if replicaSet.UID == "" || replicaSet.UID != replicaSetOwner.UID {
			return service.BootstrapWorkloadEvidence{}, errors.New("candidate Pod ReplicaSet owner UID does not match the live ReplicaSet")
		}
		deploymentOwner, ok := controllerOwner(replicaSet.OwnerReferences, "Deployment")
		if !ok {
			return service.BootstrapWorkloadEvidence{}, errors.New("candidate ReplicaSet is not owned by a Deployment controller")
		}

		declaredImage := ""
		for _, container := range pod.Spec.Containers {
			if container.Name == "worker" {
				declaredImage = container.Image
			}
		}
		if declaredImage == "" || !strings.Contains(declaredImage, "@sha256:") {
			return service.BootstrapWorkloadEvidence{}, errors.New("Worker Pod does not use an immutable digest image")
		}
		for _, container := range pod.Status.ContainerStatuses {
			if container.Name != "worker" {
				continue
			}
			runtimeImage := normalizeImageID(container.ImageID)
			if runtimeImage == "" {
				return service.BootstrapWorkloadEvidence{}, errors.New("Worker runtime imageID is unavailable")
			}
			verified := runtimeImage == declaredImage
			if !verified && strings.HasPrefix(r.cfg.Context, "kind-") {
				if err := r.imageVerifier.Verify(request.Context(), pod.Spec.NodeName, declaredImage, runtimeImage); err != nil {
					return service.BootstrapWorkloadEvidence{}, err
				}
				verified = true
			}
			if !verified {
				return service.BootstrapWorkloadEvidence{}, errors.New("runtime imageID is not exactly linked to the declared immutable image")
			}
			return service.BootstrapWorkloadEvidence{
				PodUID: podUID, ServiceAccount: serviceAccount, ObservedImage: declaredImage, RuntimeImageID: runtimeImage,
				RuntimeLinkVerified: true, AudienceVerified: true, TenantHash: tenantHash, WorkerName: workerName,
				VersionHash: versionHash, DeploymentGeneration: generation, OwnerDeployment: deploymentOwner.Name,
			}, nil
		}
		return service.BootstrapWorkloadEvidence{}, errors.New("Worker container status is unavailable")
	}
	return service.BootstrapWorkloadEvidence{}, fmt.Errorf("Pod UID %q was not found in the platform Kubernetes Namespace", podUID)
}

func controllerOwner(references []metav1.OwnerReference, kind string) (metav1.OwnerReference, bool) {
	for _, reference := range references {
		if reference.APIVersion == "apps/v1" && reference.Kind == kind && reference.Name != "" && reference.UID != "" && reference.Controller != nil && *reference.Controller {
			return reference, true
		}
	}
	return metav1.OwnerReference{}, false
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func normalizeImageID(value string) string {
	for _, prefix := range []string{"docker-pullable://", "docker://", "containerd://", "cri-o://"} {
		value = strings.TrimPrefix(value, prefix)
	}
	if !strings.Contains(value, "@sha256:") {
		return ""
	}
	return value
}
