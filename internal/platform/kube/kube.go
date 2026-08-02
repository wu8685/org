package kube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	"github.com/wu8685/org/internal/domain"
	"github.com/wu8685/org/internal/service"
)

const (
	managedByLabel = "app.kubernetes.io/managed-by"
	managedByValue = "org"
	fieldManager   = "org-control-plane"
)

type Config struct {
	Namespace, Context, Kubeconfig, WorkerTemporalAddress, TemporalNamespace string
	ReadinessTimeout                                                         time.Duration
	NetworkPolicyEnabled, NetworkPolicyEnforced                              bool
}

type Client struct {
	cfg Config
	api kubernetes.Interface
}

func New(cfg Config, api kubernetes.Interface) *Client {
	return &Client{cfg: cfg, api: api}
}

func (c *Client) Apply(ctx context.Context, deployment domain.WorkerVersion) error {
	resources, err := BuildResources(deployment, c.cfg, nil)
	if err != nil {
		return err
	}
	return c.applyResources(ctx, resources)
}

func (c *Client) ApplyBootstrap(ctx context.Context, deployment domain.WorkerVersion, bootstrap service.BootstrapDeployment) error {
	resources, err := BuildResources(deployment, c.cfg, &bootstrap)
	if err != nil {
		return err
	}
	return c.applyResources(ctx, resources)
}

func (c *Client) applyResources(ctx context.Context, resources Resources) error {
	if c.api == nil {
		return errors.New("Kubernetes API client is required")
	}
	if err := c.ensureNamespace(ctx); err != nil {
		return fmt.Errorf("ensure platform Kubernetes Namespace: %w", err)
	}
	if err := c.applyServiceAccount(ctx, resources.ServiceAccount); err != nil {
		return err
	}
	if resources.Secret != nil {
		if err := c.applySecret(ctx, resources.Secret); err != nil {
			return err
		}
	}
	if err := c.applyDeployment(ctx, resources.Deployment); err != nil {
		return err
	}
	if resources.NetworkPolicy != nil {
		if err := c.applyNetworkPolicy(ctx, resources.NetworkPolicy); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) ensureNamespace(ctx context.Context) error {
	_, err := c.api.CoreV1().Namespaces().Get(ctx, c.cfg.Namespace, metav1.GetOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	namespace := &corev1.Namespace{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Namespace"},
		ObjectMeta: metav1.ObjectMeta{Name: c.cfg.Namespace, Labels: map[string]string{managedByLabel: managedByValue}},
	}
	if _, err := c.api.CoreV1().Namespaces().Create(ctx, namespace, metav1.CreateOptions{}); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
		_, err = c.api.CoreV1().Namespaces().Get(ctx, c.cfg.Namespace, metav1.GetOptions{})
		return err
	}
	return nil
}

func (c *Client) applyServiceAccount(ctx context.Context, object *corev1.ServiceAccount) error {
	client := c.api.CoreV1().ServiceAccounts(object.Namespace)
	existing, err := client.Get(ctx, object.Name, metav1.GetOptions{})
	if err == nil {
		if err := requireOwned(existing.Labels, "ServiceAccount", object.Name); err != nil {
			return err
		}
	} else if apierrors.IsNotFound(err) {
		if _, err := client.Create(ctx, object, metav1.CreateOptions{FieldManager: fieldManager}); err == nil {
			return nil
		} else if !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("create ServiceAccount %q: %w", object.Name, err)
		}
		existing, err = client.Get(ctx, object.Name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("read concurrently created ServiceAccount %q: %w", object.Name, err)
		}
		if err := requireOwned(existing.Labels, "ServiceAccount", object.Name); err != nil {
			return err
		}
	} else {
		return fmt.Errorf("read ServiceAccount %q: %w", object.Name, err)
	}
	body, err := json.Marshal(object)
	if err != nil {
		return err
	}
	force := true
	_, err = client.Patch(ctx, object.Name, types.ApplyPatchType, body, metav1.PatchOptions{FieldManager: fieldManager, Force: &force})
	return wrapApply("ServiceAccount", object.Name, err)
}

func (c *Client) applySecret(ctx context.Context, object *corev1.Secret) error {
	client := c.api.CoreV1().Secrets(object.Namespace)
	existing, err := client.Get(ctx, object.Name, metav1.GetOptions{})
	if err == nil {
		if err := requireOwned(existing.Labels, "Secret", object.Name); err != nil {
			return err
		}
	} else if apierrors.IsNotFound(err) {
		if _, err := client.Create(ctx, object, metav1.CreateOptions{FieldManager: fieldManager}); err == nil {
			return nil
		} else if !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("create Secret %q: %w", object.Name, err)
		}
		existing, err = client.Get(ctx, object.Name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("read concurrently created Secret %q: %w", object.Name, err)
		}
		if err := requireOwned(existing.Labels, "Secret", object.Name); err != nil {
			return err
		}
	} else {
		return fmt.Errorf("read Secret %q: %w", object.Name, err)
	}
	body, err := json.Marshal(object)
	if err != nil {
		return err
	}
	force := true
	_, err = client.Patch(ctx, object.Name, types.ApplyPatchType, body, metav1.PatchOptions{FieldManager: fieldManager, Force: &force})
	return wrapApply("Secret", object.Name, err)
}

func (c *Client) applyDeployment(ctx context.Context, object *appsv1.Deployment) error {
	client := c.api.AppsV1().Deployments(object.Namespace)
	existing, err := client.Get(ctx, object.Name, metav1.GetOptions{})
	if err == nil {
		if err := requireOwned(existing.Labels, "Deployment", object.Name); err != nil {
			return err
		}
	} else if apierrors.IsNotFound(err) {
		if _, err := client.Create(ctx, object, metav1.CreateOptions{FieldManager: fieldManager}); err == nil {
			return nil
		} else if !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("create Deployment %q: %w", object.Name, err)
		}
		existing, err = client.Get(ctx, object.Name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("read concurrently created Deployment %q: %w", object.Name, err)
		}
		if err := requireOwned(existing.Labels, "Deployment", object.Name); err != nil {
			return err
		}
	} else {
		return fmt.Errorf("read Deployment %q: %w", object.Name, err)
	}
	body, err := json.Marshal(object)
	if err != nil {
		return err
	}
	force := true
	_, err = client.Patch(ctx, object.Name, types.ApplyPatchType, body, metav1.PatchOptions{FieldManager: fieldManager, Force: &force})
	return wrapApply("Deployment", object.Name, err)
}

func (c *Client) applyNetworkPolicy(ctx context.Context, object *networkingv1.NetworkPolicy) error {
	name, namespace := object.Name, object.Namespace
	client := c.api.NetworkingV1().NetworkPolicies(namespace)
	existing, err := client.Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		if err := requireOwned(existing.Labels, "NetworkPolicy", name); err != nil {
			return err
		}
	} else if apierrors.IsNotFound(err) {
		if _, err := client.Create(ctx, object, metav1.CreateOptions{FieldManager: fieldManager}); err == nil {
			return nil
		} else if !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("create NetworkPolicy %q: %w", name, err)
		}
		existing, err = client.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("read concurrently created NetworkPolicy %q: %w", name, err)
		}
		if err := requireOwned(existing.Labels, "NetworkPolicy", name); err != nil {
			return err
		}
	} else {
		return fmt.Errorf("read NetworkPolicy %q: %w", name, err)
	}
	body, err := json.Marshal(object)
	if err != nil {
		return err
	}
	force := true
	_, err = client.Patch(ctx, name, types.ApplyPatchType, body, metav1.PatchOptions{FieldManager: fieldManager, Force: &force})
	return wrapApply("NetworkPolicy", name, err)
}

func requireOwned(labels map[string]string, kind, name string) error {
	if labels[managedByLabel] != managedByValue {
		return fmt.Errorf("%s %q is not managed by org", kind, name)
	}
	return nil
}

func wrapApply(kind, name string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("apply %s %q: %w", kind, name, err)
}

func (c *Client) WaitReady(ctx context.Context, deployment domain.WorkerVersion) error {
	if c.api == nil {
		return errors.New("Kubernetes API client is required")
	}
	timeout := c.cfg.ReadinessTimeout
	if timeout == 0 {
		timeout = 2 * time.Minute
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var observedUID types.UID
	for {
		live, err := c.api.AppsV1().Deployments(c.cfg.Namespace).Get(waitCtx, workloadName(deployment), metav1.GetOptions{})
		if err != nil {
			if apierrors.IsTimeout(err) || apierrors.IsServerTimeout(err) || apierrors.IsTooManyRequests(err) {
				if err := waitReadinessInterval(waitCtx); err != nil {
					return err
				}
				continue
			}
			return fmt.Errorf("read Deployment readiness: %w", err)
		}
		if live.UID == "" {
			return errors.New("live Deployment UID is empty")
		}
		if observedUID == "" {
			observedUID = live.UID
		} else if live.UID != observedUID {
			return errors.New("Deployment was replaced while waiting for readiness")
		}
		ready, failure := deploymentReadiness(live)
		if failure != nil {
			return failure
		}
		if ready {
			return nil
		}
		if err := waitReadinessInterval(waitCtx); err != nil {
			return err
		}
	}
}

func waitReadinessInterval(ctx context.Context) error {
	timer := time.NewTimer(250 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func deploymentReadiness(deployment *appsv1.Deployment) (bool, error) {
	for _, condition := range deployment.Status.Conditions {
		if condition.Type == appsv1.DeploymentProgressing && condition.Status == corev1.ConditionFalse && condition.Reason == "ProgressDeadlineExceeded" {
			return false, fmt.Errorf("Deployment rollout failed: %s", condition.Reason)
		}
		if condition.Type == appsv1.DeploymentReplicaFailure && condition.Status == corev1.ConditionTrue {
			return false, fmt.Errorf("Deployment replica failure: %s", condition.Reason)
		}
	}
	desired := int32(1)
	if deployment.Spec.Replicas != nil {
		desired = *deployment.Spec.Replicas
	}
	return deployment.Status.ObservedGeneration >= deployment.Generation &&
		deployment.Status.UpdatedReplicas >= desired &&
		deployment.Status.AvailableReplicas >= desired &&
		deployment.Status.UnavailableReplicas == 0, nil
}

func workloadName(deployment domain.WorkerVersion) string {
	return deployment.KubernetesDeployment
}

func NetworkPolicyStatus(cfg Config) string {
	if !cfg.NetworkPolicyEnabled {
		return "disabled"
	}
	if !cfg.NetworkPolicyEnforced {
		return "manifest_only_not_enforced"
	}
	return "enforced_by_cluster_cni"
}
