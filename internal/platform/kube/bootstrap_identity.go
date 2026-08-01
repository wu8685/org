package kube

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

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

type ownerReference struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	UID        string `json:"uid"`
	Controller *bool  `json:"controller"`
}

type BootstrapEvidenceResolver struct {
	cfg    Config
	runner Runner
}

func NewBootstrapEvidenceResolver(cfg Config, runner Runner) *BootstrapEvidenceResolver {
	if runner == nil {
		runner = ExecRunner{}
	}
	return &BootstrapEvidenceResolver{cfg: cfg, runner: runner}
}

func (r *BootstrapEvidenceResolver) ResolveBootstrapEvidence(request *http.Request) (service.BootstrapWorkloadEvidence, error) {
	token := strings.TrimSpace(request.Header.Get("X-Org-Workload-Token"))
	podUID := strings.TrimSpace(request.Header.Get("X-Org-Pod-UID"))
	if token == "" || podUID == "" || r.cfg.Namespace == "" {
		return service.BootstrapWorkloadEvidence{}, errors.New("workload token, Pod UID, and platform Kubernetes Namespace are required")
	}
	reviewRequest, _ := json.Marshal(map[string]any{"apiVersion": "authentication.k8s.io/v1", "kind": "TokenReview", "spec": map[string]any{"token": token, "audiences": []string{bootstrapAudience}}})
	output, err := r.runner.Run(request.Context(), string(reviewRequest), "kubectl", append(r.flags(), "create", "--raw", "/apis/authentication.k8s.io/v1/tokenreviews", "-f", "-")...)
	if err != nil {
		return service.BootstrapWorkloadEvidence{}, err
	}
	var review struct {
		Status struct {
			Authenticated bool     `json:"authenticated"`
			Audiences     []string `json:"audiences"`
			User          struct {
				Username string              `json:"username"`
				Extra    map[string][]string `json:"extra"`
			} `json:"user"`
		} `json:"status"`
	}
	if err := json.Unmarshal(output, &review); err != nil {
		return service.BootstrapWorkloadEvidence{}, err
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
	podsJSON, err := r.runner.Run(request.Context(), "", "kubectl", append(r.flags(), "-n", r.cfg.Namespace, "get", "pods", "-l", "app.kubernetes.io/managed-by=org", "-o", "json")...)
	if err != nil {
		return service.BootstrapWorkloadEvidence{}, err
	}
	var pods struct {
		Items []struct {
			Metadata struct {
				UID             string            `json:"uid"`
				Labels          map[string]string `json:"labels"`
				OwnerReferences []ownerReference  `json:"ownerReferences"`
			} `json:"metadata"`
			Spec struct {
				ServiceAccountName string                         `json:"serviceAccountName"`
				NodeName           string                         `json:"nodeName"`
				Containers         []struct{ Name, Image string } `json:"containers"`
			} `json:"spec"`
			Status struct {
				ContainerStatuses []struct{ Name, ImageID string } `json:"containerStatuses"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(podsJSON, &pods); err != nil {
		return service.BootstrapWorkloadEvidence{}, err
	}
	for _, pod := range pods.Items {
		if pod.Metadata.UID != podUID {
			continue
		}
		if pod.Spec.ServiceAccountName != serviceAccount {
			return service.BootstrapWorkloadEvidence{}, errors.New("TokenReview ServiceAccount does not own the Pod")
		}
		tenantHash := pod.Metadata.Labels[tenantHashLabel]
		workerName := pod.Metadata.Labels[workerLabel]
		versionHash := pod.Metadata.Labels[versionHashLabel]
		generation := pod.Metadata.Labels[bootstrapGenerationLabel]
		if tenantHash == "" || workerName == "" || versionHash == "" || generation == "" {
			return service.BootstrapWorkloadEvidence{}, errors.New("candidate Pod is missing server-owned bootstrap identity labels")
		}
		replicaSetOwner, ok := controllerOwner(pod.Metadata.OwnerReferences, "ReplicaSet")
		if !ok {
			return service.BootstrapWorkloadEvidence{}, errors.New("candidate Pod is not owned by a ReplicaSet controller")
		}
		replicaSetJSON, err := r.runner.Run(request.Context(), "", "kubectl", append(r.flags(), "-n", r.cfg.Namespace, "get", "replicaset", replicaSetOwner.Name, "-o", "json")...)
		if err != nil {
			return service.BootstrapWorkloadEvidence{}, err
		}
		var replicaSet struct {
			Metadata struct {
				UID             string           `json:"uid"`
				OwnerReferences []ownerReference `json:"ownerReferences"`
			} `json:"metadata"`
		}
		if err := json.Unmarshal(replicaSetJSON, &replicaSet); err != nil {
			return service.BootstrapWorkloadEvidence{}, err
		}
		if replicaSet.Metadata.UID == "" || replicaSet.Metadata.UID != replicaSetOwner.UID {
			return service.BootstrapWorkloadEvidence{}, errors.New("candidate Pod ReplicaSet owner UID does not match the live ReplicaSet")
		}
		deploymentOwner, ok := controllerOwner(replicaSet.Metadata.OwnerReferences, "Deployment")
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
			image := normalizeImageID(container.ImageID)
			if image == "" {
				return service.BootstrapWorkloadEvidence{}, errors.New("Worker runtime imageID is unavailable")
			}
			verified := image == declaredImage
			if !verified && strings.HasPrefix(r.cfg.Context, "kind-") {
				inspect, inspectErr := r.runner.Run(request.Context(), "", "docker", "exec", pod.Spec.NodeName, "crictl", "inspecti", declaredImage)
				if inspectErr != nil {
					return service.BootstrapWorkloadEvidence{}, inspectErr
				}
				var result struct {
					Status struct {
						RepoDigests []string `json:"repoDigests"`
					} `json:"status"`
				}
				if json.Unmarshal(inspect, &result) == nil && containsString(result.Status.RepoDigests, declaredImage) && containsString(result.Status.RepoDigests, image) {
					verified = true
				}
			}
			if !verified {
				return service.BootstrapWorkloadEvidence{}, errors.New("runtime imageID is not exactly linked to the declared immutable image")
			}
			return service.BootstrapWorkloadEvidence{PodUID: podUID, ServiceAccount: serviceAccount, ObservedImage: declaredImage, RuntimeImageID: image, RuntimeLinkVerified: true, AudienceVerified: true, TenantHash: tenantHash, WorkerName: workerName, VersionHash: versionHash, DeploymentGeneration: generation, OwnerDeployment: deploymentOwner.Name}, nil
		}
		return service.BootstrapWorkloadEvidence{}, errors.New("Worker container status is unavailable")
	}
	return service.BootstrapWorkloadEvidence{}, fmt.Errorf("Pod UID %q was not found in the platform Kubernetes Namespace", podUID)
}

func controllerOwner(references []ownerReference, kind string) (ownerReference, bool) {
	for _, reference := range references {
		if reference.APIVersion == "apps/v1" && reference.Kind == kind && reference.Name != "" && reference.UID != "" && reference.Controller != nil && *reference.Controller {
			return reference, true
		}
	}
	return ownerReference{}, false
}

func (r *BootstrapEvidenceResolver) flags() []string {
	var flags []string
	if r.cfg.Context != "" {
		flags = append(flags, "--context", r.cfg.Context)
	}
	if r.cfg.Kubeconfig != "" {
		flags = append(flags, "--kubeconfig", r.cfg.Kubeconfig)
	}
	return flags
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
