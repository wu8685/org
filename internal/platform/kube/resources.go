package kube

import (
	"errors"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/wu8685/org/internal/domain"
	"github.com/wu8685/org/internal/service"
)

type Resources struct {
	ServiceAccount *corev1.ServiceAccount
	Secret         *corev1.Secret
	Deployment     *appsv1.Deployment
	NetworkPolicy  *networkingv1.NetworkPolicy
}

func BuildResources(deployment domain.WorkerVersion, cfg Config, bootstrap *service.BootstrapDeployment) (Resources, error) {
	if cfg.Namespace == "" || cfg.WorkerTemporalAddress == "" || cfg.TemporalNamespace == "" {
		return Resources{}, errors.New("kubernetes namespace and Worker Temporal connection are required")
	}
	if deployment.TenantID == "" || deployment.TenantSlug == "" || deployment.TenantHash == "" || deployment.VersionHash == "" || deployment.KubernetesDeployment == "" || deployment.KubernetesServiceAccount == "" {
		return Resources{}, errors.New("tenant-qualified canonical Kubernetes identity is required")
	}
	cpu, err := resource.ParseQuantity(deployment.Runtime.CPU)
	if err != nil {
		return Resources{}, errors.New("invalid Worker CPU quantity")
	}
	memory, err := resource.ParseQuantity(deployment.Runtime.Memory)
	if err != nil {
		return Resources{}, errors.New("invalid Worker memory quantity")
	}

	labels := workloadLabels(deployment)
	serviceAccount := &corev1.ServiceAccount{
		TypeMeta:                     metav1.TypeMeta{APIVersion: "v1", Kind: "ServiceAccount"},
		ObjectMeta:                   metav1.ObjectMeta{Name: deployment.KubernetesServiceAccount, Namespace: cfg.Namespace, Labels: cloneLabels(labels)},
		AutomountServiceAccountToken: ptr.To(false),
	}
	env := []corev1.EnvVar{
		{Name: "TEMPORAL_ADDRESS", Value: cfg.WorkerTemporalAddress},
		{Name: "TEMPORAL_NAMESPACE", Value: cfg.TemporalNamespace},
		{Name: "TEMPORAL_TASK_QUEUE", Value: deployment.TaskQueue},
		{Name: "TEMPORAL_WORKER_DEPLOYMENT", Value: deployment.WorkerDeployment},
		{Name: "TEMPORAL_WORKER_BUILD_ID", Value: deployment.Version},
	}
	for _, value := range deployment.Runtime.Environment {
		env = append(env, corev1.EnvVar{Name: value.Name, ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: value.Secret}, Key: value.SecretKey}}})
	}

	podLabels := cloneLabels(labels)
	volumeMounts := []corev1.VolumeMount{{Name: "tmp", MountPath: "/tmp"}}
	volumes := []corev1.Volume{{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}}
	var secret *corev1.Secret
	if bootstrap != nil {
		if strings.TrimSpace(bootstrap.Endpoint) == "" || strings.TrimSpace(bootstrap.Credential) == "" || strings.TrimSpace(bootstrap.Generation) == "" || bootstrap.ExpiresAt.IsZero() {
			return Resources{}, errors.New("bootstrap endpoint, credential, generation, and expiry are required")
		}
		secretName := bootstrapSecretName(deployment)
		secret = &corev1.Secret{
			TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
			ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: cfg.Namespace, Labels: cloneLabels(labels)},
			Type:       corev1.SecretTypeOpaque,
			Data:       map[string][]byte{"credential": []byte(bootstrap.Credential)},
		}
		podLabels[bootstrapGenerationLabel] = bootstrap.Generation
		env = append([]corev1.EnvVar{
			{Name: "ORG_BOOTSTRAP_ENDPOINT", Value: bootstrap.Endpoint},
			{Name: "ORG_BOOTSTRAP_TOKEN_FILE", Value: "/var/run/org-bootstrap/credential"},
			{Name: "ORG_BOOTSTRAP_WORKLOAD_TOKEN_FILE", Value: "/var/run/org-workload/token"},
			{Name: "ORG_BOOTSTRAP_POD_UID", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.uid"}}},
			{Name: "ORG_BOOTSTRAP_EXPIRES_AT", Value: bootstrap.ExpiresAt.UTC().Format(time.RFC3339)},
		}, env...)
		volumeMounts = append([]corev1.VolumeMount{
			{Name: "bootstrap", MountPath: "/var/run/org-bootstrap", ReadOnly: true},
			{Name: "workload-identity", MountPath: "/var/run/org-workload", ReadOnly: true},
		}, volumeMounts...)
		defaultMode := int32(0o440)
		expirationSeconds := int64(600)
		volumes = append([]corev1.Volume{
			{Name: "bootstrap", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: secretName, DefaultMode: &defaultMode}}},
			{Name: "workload-identity", VolumeSource: corev1.VolumeSource{Projected: &corev1.ProjectedVolumeSource{DefaultMode: &defaultMode, Sources: []corev1.VolumeProjection{{ServiceAccountToken: &corev1.ServiceAccountTokenProjection{Audience: bootstrapAudience, ExpirationSeconds: &expirationSeconds, Path: "token"}}}}}},
		}, volumes...)
	}

	deploymentObject := &appsv1.Deployment{
		TypeMeta:   metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
		ObjectMeta: metav1.ObjectMeta{Name: deployment.KubernetesDeployment, Namespace: cfg.Namespace, Labels: cloneLabels(labels)},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To[int32](1),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": deployment.KubernetesDeployment}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: mergeLabels(podLabels, map[string]string{"app.kubernetes.io/name": deployment.KubernetesDeployment})},
				Spec: corev1.PodSpec{
					ServiceAccountName:           deployment.KubernetesServiceAccount,
					AutomountServiceAccountToken: ptr.To(false),
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: ptr.To(true), FSGroup: ptr.To[int64](65532), SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
					Containers: []corev1.Container{{
						Name: "worker", Image: deployment.Image, ImagePullPolicy: corev1.PullIfNotPresent,
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: ptr.To(false), ReadOnlyRootFilesystem: ptr.To(true), Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
						},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{corev1.ResourceCPU: cpu, corev1.ResourceMemory: memory},
							Limits:   corev1.ResourceList{corev1.ResourceCPU: cpu, corev1.ResourceMemory: memory},
						},
						VolumeMounts: volumeMounts,
						Env:          env,
					}},
					Volumes: volumes,
				},
			},
		},
	}

	resources := Resources{ServiceAccount: serviceAccount, Secret: secret, Deployment: deploymentObject}
	if cfg.NetworkPolicyEnabled {
		resources.NetworkPolicy = &networkingv1.NetworkPolicy{
			TypeMeta:   metav1.TypeMeta{APIVersion: "networking.k8s.io/v1", Kind: "NetworkPolicy"},
			ObjectMeta: metav1.ObjectMeta{Name: deployment.KubernetesNetworkPolicy, Namespace: cfg.Namespace, Labels: cloneLabels(labels)},
			Spec: networkingv1.NetworkPolicySpec{
				PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{tenantHashLabel: deployment.TenantHash, workerLabel: deployment.WorkerName, versionHashLabel: deployment.VersionHash}},
				PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
				Ingress:     []networkingv1.NetworkPolicyIngressRule{},
			},
		}
	}
	return resources, nil
}

func workloadLabels(deployment domain.WorkerVersion) map[string]string {
	return map[string]string{
		managedByLabel:          managedByValue,
		"org.wu8685.dev/tenant": deployment.TenantSlug,
		tenantHashLabel:         deployment.TenantHash,
		workerLabel:             deployment.WorkerName,
		versionHashLabel:        deployment.VersionHash,
	}
}

func cloneLabels(labels map[string]string) map[string]string {
	return mergeLabels(nil, labels)
}

func mergeLabels(base, extra map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(extra))
	for key, value := range base {
		out[key] = value
	}
	for key, value := range extra {
		out[key] = value
	}
	return out
}

func bootstrapSecretName(deployment domain.WorkerVersion) string {
	base := deployment.KubernetesDeployment
	if len(base) > 53 {
		base = strings.TrimRight(base[:53], "-")
	}
	return base + "-bootstrap"
}
