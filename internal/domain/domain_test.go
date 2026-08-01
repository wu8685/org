package domain

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/wu8685/org/sdk/orgsdk"
)

func TestPublicJSONContainsWorkerNameButNoLegacyOrRuntimeRoutingIdentity(t *testing.T) {
	version := WorkerVersion{WorkerName: "payments-worker", Description: "Release note.", TaskQueue: "secret-routing", WorkerDeployment: "internal-deployment", KubernetesDeployment: "internal-k8s"}
	run := Invocation{WorkerName: "payments-worker", TaskQueue: "secret-routing", WorkerDeployment: "internal-deployment", TemporalWorkflowID: "internal-workflow", TemporalRunID: "internal-run"}
	for _, value := range []any{validWorkerVersionRequest(), version, run} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		text := string(encoded)
		if strings.Contains(text, "scope") || strings.Contains(text, "secret-routing") || strings.Contains(text, "internal-deployment") || strings.Contains(text, "internal-k8s") || strings.Contains(text, "internal-workflow") || strings.Contains(text, "internal-run") {
			t.Fatalf("public JSON leaked legacy/runtime identity: %s", text)
		}
	}
}

func TestTenantValidationAndCanonicalNamesAreStableAndCollisionSafe(t *testing.T) {
	tenantA := Tenant{ID: "tenant-01JX0A", Slug: "acme", DisplayName: "Acme", Status: TenantActive, QuotaPolicy: DefaultTenantQuotaPolicy(), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	tenantB := Tenant{ID: "tenant-01JX0B", Slug: "acme-b", DisplayName: "Acme B", Status: TenantActive, QuotaPolicy: DefaultTenantQuotaPolicy(), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := ValidateTenant(tenantA); err != nil {
		t.Fatalf("expected valid tenant: %v", err)
	}

	namesA, err := CanonicalNamesFor(tenantA, "shared-worker", "version-1", "run-1")
	if err != nil {
		t.Fatalf("names for tenant A: %v", err)
	}
	namesAAgain, err := CanonicalNamesFor(tenantA, "shared-worker", "version-1", "run-1")
	if err != nil || namesAAgain != namesA {
		t.Fatalf("canonical names must be deterministic: first=%+v second=%+v err=%v", namesA, namesAAgain, err)
	}
	namesB, err := CanonicalNamesFor(tenantB, "shared-worker", "version-1", "run-1")
	if err != nil {
		t.Fatalf("names for tenant B: %v", err)
	}
	if namesA.TaskQueue == namesB.TaskQueue || namesA.WorkerDeployment == namesB.WorkerDeployment || namesA.WorkflowID == namesB.WorkflowID || namesA.KubernetesDeployment == namesB.KubernetesDeployment || namesA.ServiceAccount == namesB.ServiceAccount {
		t.Fatalf("tenant canonical names collided: A=%+v B=%+v", namesA, namesB)
	}
	if len(namesA.KubernetesDeployment) > 63 || len(namesA.ServiceAccount) > 63 {
		t.Fatalf("Kubernetes names exceed DNS label limit: %+v", namesA)
	}
	dnsLabel := regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9]*[a-z0-9])?$`)
	if !dnsLabel.MatchString(namesA.KubernetesDeployment) || !dnsLabel.MatchString(namesA.ServiceAccount) {
		t.Fatalf("unsafe Kubernetes names: %+v", namesA)
	}
}

func TestTenantValidationRejectsMutableOrUnsafeIdentity(t *testing.T) {
	for _, tenant := range []Tenant{
		{Slug: "acme", DisplayName: "Acme", Status: TenantActive, QuotaPolicy: DefaultTenantQuotaPolicy()},
		{ID: "tenant-1", Slug: "ACME", DisplayName: "Acme", Status: TenantActive, QuotaPolicy: DefaultTenantQuotaPolicy()},
		{ID: "tenant-1", Slug: "acme", DisplayName: "Acme", Status: "unknown", QuotaPolicy: DefaultTenantQuotaPolicy()},
		{ID: "tenant-1", Slug: "acme", DisplayName: "Acme", Status: TenantActive},
	} {
		if err := ValidateTenant(tenant); err == nil {
			t.Fatalf("expected tenant validation failure for %+v", tenant)
		}
	}
}

func TestDefaultTenantQuotaPolicyIsFinite(t *testing.T) {
	policy := DefaultTenantQuotaPolicy()
	if policy.MaxReservedCPU == "" || policy.MaxReservedMemory == "" || policy.MaxActiveWorkerPods <= 0 || policy.MaxActiveReleases <= 0 || policy.MaxConcurrentRuns <= 0 || policy.MaxConcurrentDeployments <= 0 {
		t.Fatalf("unsafe default quota policy: %+v", policy)
	}
}

func TestValidateWorkerVersionRejectsMutableImageAndUnsafeWrites(t *testing.T) {
	req := validWorkerVersionRequest()
	req.Image = "registry.example.com/acme/payments:latest"
	if err := ValidateWorkerVersion(req, []string{"registry.example.com"}); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("expected digest validation error, got %v", err)
	}

	req = validWorkerVersionRequest()
	req.Metadata.Activities[0].IdempotencyKey = nil
	if err := ValidateWorkerVersion(req, []string{"registry.example.com"}); err == nil || !strings.Contains(err.Error(), "idempotency") {
		t.Fatalf("expected idempotency validation error, got %v", err)
	}
}

func TestValidateWorkerVersionPublishAcceptsOnlyCanonicalPlatformDigestAndNoContract(t *testing.T) {
	req := WorkerVersionPublishRequest{
		WorkerName: "payments-worker", Description: "Charges payment orders.",
		Image: "registry.example.com/acme/payments@sha256:" + strings.Repeat("a", 64), Version: "2026.08.1",
		Runtime: RuntimeSpec{CPU: "100m", Memory: "128Mi"},
		Source:  SourceProvenance{Repository: "https://github.com/acme/payments", Branch: "main", Commit: "abcdef1234567", CIReference: "build-42"},
	}
	if err := ValidateWorkerVersionPublish(req, []string{"registry.example.com"}); err != nil {
		t.Fatalf("valid pending release: %v", err)
	}
	for _, image := range []string{
		"registry.example.com/acme/payments:latest",
		"registry.example.com/acme/payments:v1@sha256:" + strings.Repeat("a", 64),
		"registry.example.com/acme/payments@sha256:" + strings.Repeat("A", 64),
		"registry.example.com@sha256:" + strings.Repeat("a", 64),
	} {
		req.Image = image
		if err := ValidateWorkerVersionPublish(req, []string{"registry.example.com"}); err == nil || !strings.Contains(err.Error(), "immutable") {
			t.Fatalf("image %q accepted: %v", image, err)
		}
	}
}

func TestValidateRegisteredContractIsSeparateFromPublishInput(t *testing.T) {
	req := dynamicWorkerVersionRequest(t)
	registration := WorkerContractRegistration{ManifestDigest: req.ManifestDigest, Metadata: req.Metadata, BuildID: req.Version}
	if err := ValidateWorkerContractRegistration(registration, req.Version); err != nil {
		t.Fatalf("valid registration: %v", err)
	}
	registration.BuildID = "other"
	if err := ValidateWorkerContractRegistration(registration, req.Version); err == nil || !strings.Contains(err.Error(), "Build ID") {
		t.Fatalf("mismatched build ID accepted: %v", err)
	}
}

func TestValidateWorkerVersionAcceptsExplicitReconciliation(t *testing.T) {
	req := validWorkerVersionRequest()
	req.Metadata.Activities[0].IdempotencyKey = nil
	req.Metadata.Activities[0].ReconciliationPolicy = "query downstream by order ID, then compensate with refund"
	req.Metadata.Activities[0].RetryPolicy.MaximumAttempts = 1
	if err := ValidateWorkerVersion(req, []string{"registry.example.com"}); err != nil {
		t.Fatalf("expected valid request, got %v", err)
	}
}

func TestValidateWorkerVersionRequiresPinnedProjectionContract(t *testing.T) {
	req := validWorkerVersionRequest()
	req.Metadata.Workflows[0].VersioningBehavior = "auto-upgrade"
	req.Metadata.Workflows[0].ProjectionQuery = ""
	err := ValidateWorkerVersion(req, []string{"registry.example.com"})
	if err == nil || !strings.Contains(err.Error(), "pinned") || !strings.Contains(err.Error(), "projection") {
		t.Fatalf("expected pinned/projection errors, got %v", err)
	}
}

func TestValidateWorkerVersionRejectsCyclicDAGAndUnsafeRuntimeReference(t *testing.T) {
	req := validWorkerVersionRequest()
	req.Metadata.Workflows[0].Steps = []DAGStep{{ID: "a", Label: "A", DependsOn: []string{"b"}}, {ID: "b", Label: "B", DependsOn: []string{"a"}}}
	req.Runtime.Environment = []EnvReference{{Name: "TOKEN\nINJECT", Secret: "payments", SecretKey: "token"}}
	err := ValidateWorkerVersion(req, []string{"registry.example.com"})
	if err == nil || !strings.Contains(err.Error(), "cycle") || !strings.Contains(err.Error(), "environment reference") {
		t.Fatalf("expected DAG/runtime errors, got %v", err)
	}
}

func TestWorkerVersionDescriptionIsRequiredPlainText(t *testing.T) {
	req := validWorkerVersionRequest()
	for _, description := range []string{"", strings.Repeat("x", 2001), "unsafe\x00description"} {
		req.Description = description
		if err := ValidateWorkerVersion(req, []string{"registry.example.com"}); err == nil || !strings.Contains(err.Error(), "description") {
			t.Fatalf("expected description rejection for %q: %v", description, err)
		}
	}
}

func TestWorkerVersionRejectsInvalidActionContract(t *testing.T) {
	req := validWorkerVersionRequest()
	req.Metadata.Workflows[0].Actions = []ActionContract{{Name: "confirm", Label: "Confirm", NodeTemplateID: "approval"}}
	if err := ValidateWorkerVersion(req, []string{"registry.example.com"}); err == nil || !strings.Contains(err.Error(), "permission") {
		t.Fatalf("invalid action contract = %v", err)
	}
	req.Metadata.Workflows[0].Actions[0].RequiredPermission = "run:action:confirm"
	req.Metadata.Workflows[0].Actions[0].InputSchema = []byte(`{"type":`)
	if err := ValidateWorkerVersion(req, []string{"registry.example.com"}); err == nil || !strings.Contains(err.Error(), "schema") {
		t.Fatalf("invalid action schema = %v", err)
	}
}

func TestValidateWorkerVersionAcceptsGeneratedOrgSDKManifestWithoutStaticSteps(t *testing.T) {
	manifest, digest, err := orgsdk.GenerateManifest("DynamicWorkflow", orgsdk.Definition{
		Name: "dynamic-workflow",
		Templates: []orgsdk.NodeTemplate{
			{
				ID: "route", Label: "Choose route", Type: orgsdk.NodeTypeActivity,
				Activity: &orgsdk.ActivityPolicy{
					SideEffect: orgsdk.SideEffectRead,
					Retry:      orgsdk.RetryPolicy{MaximumAttempts: 3, StartToCloseTimeout: time.Minute},
				},
			},
			{
				ID: "approval", Label: "Approve", Type: orgsdk.NodeTypeWaitForAction,
				Actions: []orgsdk.ActionDefinition{{
					Name: "confirm", Label: "Confirm", RequiredPermission: "run:action:confirm",
					InputSchema: json.RawMessage(`{"type":"object"}`),
				}},
			},
		},
		Bounds: orgsdk.RuntimeBounds{MaxInstancesPerFanOut: 10, MaxRuntimeNodes: 100, MaxProjectionBytes: 64 * 1024},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := validWorkerVersionRequest()
	req.ManifestDigest = digest
	req.Metadata = WorkerMetadata{}
	if err := json.Unmarshal(manifest, &req.Metadata); err != nil {
		t.Fatal(err)
	}
	if len(req.Metadata.Workflows[0].Steps) != 0 {
		t.Fatal("generated SDK manifest unexpectedly contains legacy static steps")
	}
	if err := ValidateWorkerVersion(req, []string{"registry.example.com"}); err != nil {
		t.Fatalf("generated Org SDK manifest must be accepted: %v", err)
	}
}

func TestValidateWorkerVersionRejectsOrgSDKManifestDigestMismatch(t *testing.T) {
	req := dynamicWorkerVersionRequest(t)
	req.ManifestDigest = "sha256:" + strings.Repeat("f", 64)
	if err := ValidateWorkerVersion(req, []string{"registry.example.com"}); err == nil || !strings.Contains(err.Error(), "manifest digest") {
		t.Fatalf("manifest digest mismatch error = %v", err)
	}
}

func TestValidateWorkerVersionAcceptsVersionConfigButRejectsIdentityOverrides(t *testing.T) {
	req := dynamicWorkerVersionRequest(t)
	req.VersionConfig = json.RawMessage(`{"region":"local","provider":{"secretRef":"provider-token"}}`)
	if err := ValidateWorkerVersion(req, []string{"registry.example.com"}); err != nil {
		t.Fatal(err)
	}
	for _, config := range []string{
		`[]`, `{"scope":"legacy"}`, `{"tenantId":"tenant-b"}`, `{"taskQueue":"forged"}`, `{"workerDeployment":"forged"}`,
	} {
		req.VersionConfig = json.RawMessage(config)
		if err := ValidateWorkerVersion(req, []string{"registry.example.com"}); err == nil {
			t.Fatalf("versionConfig %s was accepted", config)
		}
	}
}

func TestValidateWorkerVersionRejectsUnsafeDynamicManifest(t *testing.T) {
	req := dynamicWorkerVersionRequest(t)
	req.Metadata.Workflows[0].RuntimeBounds.MaxRuntimeNodes = 0
	req.Metadata.Workflows[0].NodeTemplates[0].Activity.Idempotency = nil
	err := ValidateWorkerVersion(req, []string{"registry.example.com"})
	if err == nil || !strings.Contains(err.Error(), "runtime bounds") || !strings.Contains(err.Error(), "idempotency or reconciliation") {
		t.Fatalf("unsafe dynamic manifest error = %v", err)
	}
}

func TestValidateWorkerVersionRejectsActivityManifestDriftFromNodeTemplate(t *testing.T) {
	req := dynamicWorkerVersionRequest(t)
	if len(req.Metadata.Activities) != 1 || req.Metadata.Activities[0].Policy == nil {
		t.Fatalf("generated activities = %#v", req.Metadata.Activities)
	}
	req.Metadata.Activities[0].Policy.SideEffect = "read"
	digest, err := workerMetadataDigest(req.Metadata)
	if err != nil {
		t.Fatal(err)
	}
	req.ManifestDigest = digest
	err = ValidateWorkerVersion(req, []string{"registry.example.com"})
	if err == nil || !strings.Contains(err.Error(), "activity manifest") {
		t.Fatalf("activity manifest drift error = %v", err)
	}
}

func TestValidateWorkerVersionRejectsUnsupportedOrgSDKProtocol(t *testing.T) {
	req := dynamicWorkerVersionRequest(t)
	req.Metadata.SDK.RuntimeProtocolVersion = "99"
	digest, err := workerMetadataDigest(req.Metadata)
	if err != nil {
		t.Fatal(err)
	}
	req.ManifestDigest = digest
	if err := ValidateWorkerVersion(req, []string{"registry.example.com"}); err == nil || !strings.Contains(err.Error(), "unsupported SDK") {
		t.Fatalf("unsupported SDK protocol error = %v", err)
	}
}

func dynamicWorkerVersionRequest(t *testing.T) WorkerVersionRequest {
	t.Helper()
	manifest, digest, err := orgsdk.GenerateManifest("DynamicWorkflow", orgsdk.Definition{
		Name: "dynamic-workflow",
		Templates: []orgsdk.NodeTemplate{{
			ID: "write", Label: "Write", Type: orgsdk.NodeTypeActivity,
			Activity: &orgsdk.ActivityPolicy{
				SideEffect:  orgsdk.SideEffectWrite,
				Retry:       orgsdk.RetryPolicy{MaximumAttempts: 3, StartToCloseTimeout: time.Minute},
				Idempotency: &orgsdk.IdempotencyPolicy{BusinessKeyRequired: true, PropagationField: "operationId"},
			},
		}},
		Bounds: orgsdk.RuntimeBounds{MaxInstancesPerFanOut: 10, MaxRuntimeNodes: 100, MaxProjectionBytes: 64 * 1024},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := validWorkerVersionRequest()
	req.ManifestDigest = digest
	req.Metadata = WorkerMetadata{}
	if err := json.Unmarshal(manifest, &req.Metadata); err != nil {
		t.Fatal(err)
	}
	return req
}

func validWorkerVersionRequest() WorkerVersionRequest {
	return WorkerVersionRequest{
		WorkerName: "payments-worker", Description: "Charges payment orders.", Image: "registry.example.com/acme/payments@sha256:" + strings.Repeat("a", 64),
		Version: "2026.08.1",
		Metadata: WorkerMetadata{
			Workflows: []WorkflowContract{{
				Name: "ChargeOrder", VersioningBehavior: "pinned", ProjectionQuery: "org_projection",
				Steps: []DAGStep{{ID: "authorize", Label: "Authorize", AllowedActions: []string{"cancel"}}},
			}},
			Activities: []ActivityContract{{
				Name: "ChargeCard", Kind: "write",
				IdempotencyKey: &IdempotencyKeyContract{Field: "request_id", Derivation: "workflow_id/activity_id"},
				RetryPolicy:    RetryPolicy{MaximumAttempts: 3},
			}},
		},
		Runtime: RuntimeSpec{CPU: "250m", Memory: "256Mi", ServiceAccount: "payments-worker"},
		Source:  SourceProvenance{Repository: "https://example.com/acme/payments", Branch: "main", Commit: strings.Repeat("b", 40), CIReference: "build-42"},
	}
}
