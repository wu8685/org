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
				Username string `json:"username"`
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
	serviceAccount := strings.TrimPrefix(review.Status.User.Username, wantPrefix)
	podsJSON, err := r.runner.Run(request.Context(), "", "kubectl", append(r.flags(), "-n", r.cfg.Namespace, "get", "pods", "-l", "app.kubernetes.io/managed-by=org", "-o", "json")...)
	if err != nil {
		return service.BootstrapWorkloadEvidence{}, err
	}
	var pods struct {
		Items []struct {
			Metadata struct {
				UID string `json:"uid"`
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
			return service.BootstrapWorkloadEvidence{PodUID: podUID, ServiceAccount: serviceAccount, ObservedImage: declaredImage, RuntimeImageID: image, RuntimeLinkVerified: true, AudienceVerified: true}, nil
		}
		return service.BootstrapWorkloadEvidence{}, errors.New("Worker container status is unavailable")
	}
	return service.BootstrapWorkloadEvidence{}, fmt.Errorf("Pod UID %q was not found in the platform Kubernetes Namespace", podUID)
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
