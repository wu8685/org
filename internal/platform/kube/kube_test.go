package kube

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"

	"github.com/wu8685/org/internal/domain"
	"github.com/wu8685/org/internal/service"
)

func TestBuildResourcesAreTypedDigestPinnedAndConstrained(t *testing.T) {
	d := testDeployment()
	d.Runtime.ServiceAccount = "forged-client-service-account"
	resources, err := BuildResources(d, Config{Namespace: "org-platform", WorkerTemporalAddress: "temporal.org.local:7233", TemporalNamespace: "platform", NetworkPolicyEnabled: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resources.ServiceAccount.Name != d.KubernetesServiceAccount || resources.Deployment.Name != d.KubernetesDeployment || resources.NetworkPolicy == nil || resources.Secret != nil {
		t.Fatalf("unexpected resources: %#v", resources)
	}
	if resources.ServiceAccount.AutomountServiceAccountToken == nil || *resources.ServiceAccount.AutomountServiceAccountToken {
		t.Fatal("Worker ServiceAccount token is automounted")
	}
	pod := resources.Deployment.Spec.Template.Spec
	if pod.ServiceAccountName != d.KubernetesServiceAccount || pod.ServiceAccountName == d.Runtime.ServiceAccount {
		t.Fatalf("service account = %q", pod.ServiceAccountName)
	}
	container := pod.Containers[0]
	if container.Image != d.Image || container.Resources.Requests.Cpu().String() != "250m" || container.Resources.Limits.Memory().String() != "256Mi" {
		t.Fatalf("container identity/resources = %#v", container)
	}
	if container.SecurityContext == nil || container.SecurityContext.AllowPrivilegeEscalation == nil || *container.SecurityContext.AllowPrivilegeEscalation || container.SecurityContext.ReadOnlyRootFilesystem == nil || !*container.SecurityContext.ReadOnlyRootFilesystem {
		t.Fatalf("unsafe container security context: %#v", container.SecurityContext)
	}
	if pod.SecurityContext == nil || pod.SecurityContext.RunAsNonRoot == nil || !*pod.SecurityContext.RunAsNonRoot || pod.SecurityContext.SeccompProfile == nil || pod.SecurityContext.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Fatalf("unsafe Pod security context: %#v", pod.SecurityContext)
	}
	if got := resources.Deployment.Labels[tenantHashLabel]; got != d.TenantHash {
		t.Fatalf("tenant hash label = %q", got)
	}
}

func TestBuildBootstrapResourcesInjectCredentialAsReadonlyFile(t *testing.T) {
	bootstrap := service.BootstrapDeployment{Endpoint: "https://host.docker.internal:8090/internal/v1/bootstrap/register", Credential: "opaque-secret", Generation: "generation-random", ExpiresAt: time.Now().Add(time.Minute)}
	resources, err := BuildResources(testDeployment(), Config{Namespace: "org-workers", WorkerTemporalAddress: "host.docker.internal:7233", TemporalNamespace: "default"}, &bootstrap)
	if err != nil {
		t.Fatal(err)
	}
	if resources.Secret == nil || string(resources.Secret.Data["credential"]) != bootstrap.Credential {
		t.Fatal("bootstrap credential was not stored in the typed Secret")
	}
	pod := resources.Deployment.Spec.Template
	if pod.Labels[bootstrapGenerationLabel] != bootstrap.Generation {
		t.Fatalf("bootstrap generation = %q", pod.Labels[bootstrapGenerationLabel])
	}
	container := pod.Spec.Containers[0]
	for _, name := range []string{"ORG_BOOTSTRAP_ENDPOINT", "ORG_BOOTSTRAP_TOKEN_FILE", "ORG_BOOTSTRAP_WORKLOAD_TOKEN_FILE", "ORG_BOOTSTRAP_POD_UID", "ORG_BOOTSTRAP_EXPIRES_AT"} {
		if !hasEnv(container.Env, name) {
			t.Errorf("missing bootstrap env %s", name)
		}
	}
	if hasEnv(container.Env, "ORG_TENANT") || hasEnv(container.Env, "ORG_WORKER_NAME") || hasEnv(container.Env, "ORG_WORKER_VERSION") {
		t.Fatal("public target identity was injected into the Worker")
	}
	if !hasReadonlyMount(container.VolumeMounts, "bootstrap") || !hasReadonlyMount(container.VolumeMounts, "workload-identity") {
		t.Fatalf("bootstrap mounts are not readonly: %#v", container.VolumeMounts)
	}
}

func TestApplyCreatesNamespaceAndUsesServerSideApply(t *testing.T) {
	d := testDeployment()
	owned := map[string]string{managedByLabel: managedByValue}
	api := fake.NewClientset(
		&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: d.KubernetesServiceAccount, Namespace: "org-workers", Labels: owned}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: bootstrapSecretName(d), Namespace: "org-workers", Labels: owned}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: d.KubernetesDeployment, Namespace: "org-workers", Labels: owned}},
	)
	var patches []ktesting.PatchAction
	api.PrependReactor("patch", "*", func(action ktesting.Action) (bool, runtime.Object, error) {
		patch := action.(ktesting.PatchAction)
		patches = append(patches, patch)
		var object runtime.Object
		switch action.GetResource().Resource {
		case "serviceaccounts":
			object = &corev1.ServiceAccount{}
		case "secrets":
			object = &corev1.Secret{}
		case "deployments":
			object = &appsv1.Deployment{}
		default:
			t.Fatalf("unexpected patch resource: %s", action.GetResource().Resource)
		}
		return true, object, nil
	})
	client := New(Config{Namespace: "org-workers", WorkerTemporalAddress: "host.docker.internal:7233", TemporalNamespace: "default"}, api)
	if err := client.ApplyBootstrap(context.Background(), d, service.BootstrapDeployment{Endpoint: "http://host.docker.internal:8090/internal/v1/bootstrap/register", Credential: "secret", Generation: "generation-1", ExpiresAt: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	namespace, err := api.CoreV1().Namespaces().Get(context.Background(), "org-workers", metav1.GetOptions{})
	if err != nil || namespace.Labels[managedByLabel] != managedByValue {
		t.Fatalf("created Namespace = %#v, error=%v", namespace, err)
	}
	if len(patches) != 3 {
		t.Fatalf("patches = %d, want ServiceAccount, Secret, Deployment", len(patches))
	}
	for _, patch := range patches {
		if patch.GetPatchType() != types.ApplyPatchType {
			t.Fatalf("patch type = %q", patch.GetPatchType())
		}
		var body map[string]any
		if err := json.Unmarshal(patch.GetPatch(), &body); err != nil || body["apiVersion"] == nil || body["kind"] == nil {
			t.Fatalf("invalid apply body %s: %v", patch.GetPatch(), err)
		}
		options := patch.(ktesting.PatchActionImpl).GetPatchOptions()
		if options.FieldManager != fieldManager || options.Force == nil || !*options.Force {
			t.Fatalf("patch options = %#v", options)
		}
	}
}

func TestApplyCreatesMissingTypedResourcesWithFieldManager(t *testing.T) {
	api := fake.NewClientset(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "org-workers"}})
	client := New(Config{Namespace: "org-workers", WorkerTemporalAddress: "temporal:7233", TemporalNamespace: "default"}, api)
	if err := client.Apply(context.Background(), testDeployment()); err != nil {
		t.Fatal(err)
	}
	created := 0
	for _, action := range api.Actions() {
		if action.GetVerb() != "create" || action.GetResource().Resource == "namespaces" {
			continue
		}
		created++
		options := action.(ktesting.CreateActionImpl).GetCreateOptions()
		if options.FieldManager != fieldManager {
			t.Fatalf("create options = %#v", options)
		}
	}
	if created != 2 {
		t.Fatalf("created resources = %d, want ServiceAccount and Deployment", created)
	}
}

func TestApplyDoesNotAdoptConcurrentlyCreatedUnownedResource(t *testing.T) {
	api := fake.NewClientset(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "org-workers"}})
	api.PrependReactor("create", "serviceaccounts", func(action ktesting.Action) (bool, runtime.Object, error) {
		created := action.(ktesting.CreateAction).GetObject().(*corev1.ServiceAccount).DeepCopy()
		created.Labels = nil
		if err := api.Tracker().Create(corev1.SchemeGroupVersion.WithResource("serviceaccounts"), created, created.Namespace); err != nil {
			t.Fatal(err)
		}
		return true, nil, apierrors.NewAlreadyExists(schema.GroupResource{Resource: "serviceaccounts"}, created.Name)
	})
	client := New(Config{Namespace: "org-workers", WorkerTemporalAddress: "temporal:7233", TemporalNamespace: "default"}, api)
	err := client.Apply(context.Background(), testDeployment())
	if err == nil || !strings.Contains(err.Error(), "not managed by org") {
		t.Fatalf("error = %v", err)
	}
	for _, action := range api.Actions() {
		if action.GetResource().Resource == "deployments" && (action.GetVerb() == "create" || action.GetVerb() == "patch") {
			t.Fatalf("deployment mutation followed ownership race: %#v", action)
		}
	}
}

func TestApplyPreservesExistingNamespaceAndRejectsUnownedWorkload(t *testing.T) {
	api := fake.NewClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "shared-existing"}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: testDeployment().KubernetesDeployment, Namespace: "shared-existing"}},
	)
	client := New(Config{Namespace: "shared-existing", WorkerTemporalAddress: "temporal:7233", TemporalNamespace: "default"}, api)
	err := client.Apply(context.Background(), testDeployment())
	if err == nil || !strings.Contains(err.Error(), "not managed by org") {
		t.Fatalf("error = %v", err)
	}
	namespace, getErr := api.CoreV1().Namespaces().Get(context.Background(), "shared-existing", metav1.GetOptions{})
	if getErr != nil || len(namespace.Labels) != 0 {
		t.Fatalf("existing Namespace was adopted: %#v, error=%v", namespace, getErr)
	}
}

func TestApplyDoesNotCreateNamespaceAfterForbiddenRead(t *testing.T) {
	api := fake.NewClientset()
	api.PrependReactor("get", "namespaces", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "namespaces"}, "org-workers", errors.New("denied"))
	})
	client := New(Config{Namespace: "org-workers", WorkerTemporalAddress: "temporal:7233", TemporalNamespace: "default"}, api)
	err := client.Apply(context.Background(), testDeployment())
	if !apierrors.IsForbidden(err) {
		t.Fatalf("error = %v", err)
	}
	for _, action := range api.Actions() {
		if action.GetVerb() == "create" || action.GetVerb() == "patch" {
			t.Fatalf("mutation followed forbidden Namespace read: %#v", action)
		}
	}
}

func TestApplyConvergesWhenNamespaceIsCreatedConcurrently(t *testing.T) {
	api := fake.NewClientset()
	createCalls := 0
	api.PrependReactor("create", "namespaces", func(action ktesting.Action) (bool, runtime.Object, error) {
		createCalls++
		if createCalls == 1 {
			created := action.(ktesting.CreateAction).GetObject().(*corev1.Namespace).DeepCopy()
			if err := api.Tracker().Create(corev1.SchemeGroupVersion.WithResource("namespaces"), created, ""); err != nil {
				t.Fatal(err)
			}
			return true, nil, apierrors.NewAlreadyExists(schema.GroupResource{Resource: "namespaces"}, created.Name)
		}
		return false, nil, nil
	})
	api.PrependReactor("patch", "*", successfulPatchReactor)
	client := New(Config{Namespace: "org-workers", WorkerTemporalAddress: "temporal:7233", TemporalNamespace: "default"}, api)
	if err := client.Apply(context.Background(), testDeployment()); err != nil {
		t.Fatal(err)
	}
}

func TestWaitReadyUsesDeploymentStatusAndHonorsContext(t *testing.T) {
	d := testDeployment()
	replicas := int32(1)
	ready := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: d.KubernetesDeployment, Namespace: "org-workers", UID: "deployment-1", Generation: 2},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
		Status:     appsv1.DeploymentStatus{ObservedGeneration: 2, UpdatedReplicas: 1, AvailableReplicas: 1, ReadyReplicas: 1},
	}
	client := New(Config{Namespace: "org-workers", ReadinessTimeout: time.Second}, fake.NewClientset(ready))
	if err := client.WaitReady(context.Background(), d); err != nil {
		t.Fatal(err)
	}

	pending := ready.DeepCopy()
	pending.Status = appsv1.DeploymentStatus{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := New(Config{Namespace: "org-workers", ReadinessTimeout: time.Second}, fake.NewClientset(pending)).WaitReady(ctx, d)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}

func TestWaitReadyRejectsProgressDeadlineExceeded(t *testing.T) {
	d := testDeployment()
	replicas := int32(1)
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: d.KubernetesDeployment, Namespace: "org-workers", UID: "deployment-1", Generation: 2},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas, Strategy: appsv1.DeploymentStrategy{RollingUpdate: &appsv1.RollingUpdateDeployment{MaxUnavailable: &intstr.IntOrString{Type: intstr.Int, IntVal: 0}}}},
		Status:     appsv1.DeploymentStatus{ObservedGeneration: 2, Conditions: []appsv1.DeploymentCondition{{Type: appsv1.DeploymentProgressing, Status: corev1.ConditionFalse, Reason: "ProgressDeadlineExceeded"}}},
	}
	err := New(Config{Namespace: "org-workers", ReadinessTimeout: time.Second}, fake.NewClientset(deployment)).WaitReady(context.Background(), d)
	if err == nil || !strings.Contains(err.Error(), "ProgressDeadlineExceeded") {
		t.Fatalf("error = %v", err)
	}
}

func TestWaitReadyRetriesTransientAPIServerFailure(t *testing.T) {
	d := testDeployment()
	replicas := int32(1)
	ready := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: d.KubernetesDeployment, Namespace: "org-workers", UID: "deployment-1", Generation: 1},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
		Status:     appsv1.DeploymentStatus{ObservedGeneration: 1, UpdatedReplicas: 1, AvailableReplicas: 1},
	}
	api := fake.NewClientset(ready)
	calls := 0
	api.PrependReactor("get", "deployments", func(ktesting.Action) (bool, runtime.Object, error) {
		calls++
		if calls == 1 {
			return true, nil, apierrors.NewServerTimeout(schema.GroupResource{Group: "apps", Resource: "deployments"}, "get", 1)
		}
		return false, nil, nil
	})
	if err := New(Config{Namespace: "org-workers", ReadinessTimeout: time.Second}, api).WaitReady(context.Background(), d); err != nil {
		t.Fatal(err)
	}
}

func TestNetworkPolicyStatus(t *testing.T) {
	if got := NetworkPolicyStatus(Config{NetworkPolicyEnabled: true}); got != "manifest_only_not_enforced" {
		t.Fatalf("status = %q", got)
	}
}

func successfulPatchReactor(action ktesting.Action) (bool, runtime.Object, error) {
	switch action.GetResource().Resource {
	case "serviceaccounts":
		return true, &corev1.ServiceAccount{}, nil
	case "secrets":
		return true, &corev1.Secret{}, nil
	case "deployments":
		return true, &appsv1.Deployment{}, nil
	default:
		return false, nil, nil
	}
}

func hasEnv(values []corev1.EnvVar, name string) bool {
	for _, value := range values {
		if value.Name == name {
			return true
		}
	}
	return false
}

func hasReadonlyMount(values []corev1.VolumeMount, name string) bool {
	for _, value := range values {
		if value.Name == name && value.ReadOnly {
			return true
		}
	}
	return false
}

func testDeployment() domain.WorkerVersion {
	return domain.WorkerVersion{ID: "ver-1", TenantID: "tenant-a", TenantSlug: "alpha", TenantHash: "h2m4abc123", VersionHash: "version123", WorkerName: "payments-worker", Version: "v1", Image: "registry.example.com/acme/payments@sha256:" + strings.Repeat("a", 64), TaskQueue: "org-alpha-payments-worker-hash123456", WorkerDeployment: "org-alpha-payments-worker-hash123456", KubernetesDeployment: "org-alpha-payments-worker-hash123456-version123", KubernetesServiceAccount: "org-alpha-payments-worker-hash123456", KubernetesNetworkPolicy: "org-alpha-payments-worker-np-version123", Runtime: domain.RuntimeSpec{CPU: "250m", Memory: "256Mi", ServiceAccount: "org-alpha-payments-worker-hash123456"}}
}
