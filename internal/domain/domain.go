package domain

import (
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var (
	namePattern           = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$`)
	digestPattern         = regexp.MustCompile(`^[^/@\s]+(?:/[^/@\s]+)+@sha256:[a-fA-F0-9]{64}$`)
	manifestDigestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	commitPattern         = regexp.MustCompile(`^[a-fA-F0-9]{7,64}$`)
	versionPattern        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
	envNamePattern        = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	secretKeyPattern      = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	quantityPattern       = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?(?:m|[KMGTPE]i?)?$`)
	tenantSlugPattern     = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9]{0,38}[a-z0-9])?$`)
	permissionPattern     = regexp.MustCompile(`^[a-z0-9][a-z0-9:._-]{0,127}$`)
	dynamicIDPattern      = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._/-]*[a-z0-9])?$`)
)

const (
	OrgSDKContractVersion        = "org.worker/v1"
	OrgSDKProjectionEventVersion = "1"
	OrgSDKDynamicNodeIDVersion   = "1"
	OrgSDKProjectionQuery        = "org.sdk/projection"
	OrgSDKModuleVersion          = "0.1.0"
	OrgSDKRuntimeProtocolVersion = "1"
)

type TenantStatus string

const (
	TenantActive    TenantStatus = "active"
	TenantSuspended TenantStatus = "suspended"
	TenantDeleting  TenantStatus = "deleting"
)

type TenantQuotaPolicy struct {
	MaxReservedCPU           string `json:"maxReservedCPU"`
	MaxReservedMemory        string `json:"maxReservedMemory"`
	MaxActiveWorkerPods      int    `json:"maxActiveWorkerPods"`
	MaxActiveReleases        int    `json:"maxActiveReleases"`
	MaxConcurrentRuns        int    `json:"maxConcurrentRuns"`
	MaxConcurrentDeployments int    `json:"maxConcurrentDeployments"`
}

func DefaultTenantQuotaPolicy() TenantQuotaPolicy {
	return TenantQuotaPolicy{
		MaxReservedCPU: "2", MaxReservedMemory: "2Gi", MaxActiveWorkerPods: 4,
		MaxActiveReleases: 4, MaxConcurrentRuns: 16, MaxConcurrentDeployments: 1,
	}
}

type QuotaLeaseKind string

const (
	QuotaLeaseRelease    QuotaLeaseKind = "release"
	QuotaLeaseRun        QuotaLeaseKind = "run"
	QuotaLeaseDeployment QuotaLeaseKind = "deployment"
)

type QuotaLease struct {
	ID                    string         `json:"id"`
	TenantID              string         `json:"tenantId"`
	Kind                  QuotaLeaseKind `json:"kind"`
	ReservedCPUMilli      int64          `json:"reservedCpuMilli"`
	ReservedMemoryBytes   int64          `json:"reservedMemoryBytes"`
	ActiveWorkerPods      int            `json:"activeWorkerPods"`
	ActiveReleases        int            `json:"activeReleases"`
	ConcurrentRuns        int            `json:"concurrentRuns"`
	ConcurrentDeployments int            `json:"concurrentDeployments"`
	CreatedAt             time.Time      `json:"createdAt"`
}

type Tenant struct {
	ID          string            `json:"id"`
	Slug        string            `json:"slug"`
	DisplayName string            `json:"displayName"`
	Status      TenantStatus      `json:"status"`
	QuotaPolicy TenantQuotaPolicy `json:"quotaPolicy"`
	CreatedAt   time.Time         `json:"createdAt"`
	UpdatedAt   time.Time         `json:"updatedAt"`
}

type CanonicalNames struct {
	TenantWorkerKey      string
	TenantHash           string
	VersionHash          string
	TaskQueue            string
	WorkerDeployment     string
	WorkflowID           string
	KubernetesDeployment string
	ServiceAccount       string
	NetworkPolicy        string
}

func ValidateTenant(tenant Tenant) error {
	if strings.TrimSpace(tenant.ID) == "" {
		return errors.New("tenant ID is required")
	}
	if !tenantSlugPattern.MatchString(tenant.Slug) {
		return errors.New("tenant slug must be a lower-case DNS label of at most 40 characters")
	}
	if strings.TrimSpace(tenant.DisplayName) == "" {
		return errors.New("tenant display name is required")
	}
	quota := tenant.QuotaPolicy
	if !quantityPattern.MatchString(quota.MaxReservedCPU) || !quantityPattern.MatchString(quota.MaxReservedMemory) || quota.MaxReservedCPU == "0" || quota.MaxReservedCPU == "0m" || quota.MaxReservedMemory == "0" || quota.MaxActiveWorkerPods <= 0 || quota.MaxActiveReleases <= 0 || quota.MaxConcurrentRuns <= 0 || quota.MaxConcurrentDeployments <= 0 {
		return errors.New("tenant quota policy must contain finite positive limits")
	}
	switch tenant.Status {
	case TenantActive, TenantSuspended, TenantDeleting:
		return nil
	default:
		return errors.New("tenant status is invalid")
	}
}

func CanonicalNamesFor(tenant Tenant, workerName, versionID, opaqueRunID string) (CanonicalNames, error) {
	if err := ValidateTenant(tenant); err != nil {
		return CanonicalNames{}, err
	}
	if !namePattern.MatchString(workerName) || strings.Contains(workerName, ".") {
		return CanonicalNames{}, errors.New("worker name must be a lower-case DNS label")
	}
	if versionID == "" || opaqueRunID == "" {
		return CanonicalNames{}, errors.New("version ID and opaque run ID are required")
	}
	tenantHash := stableHash(tenant.ID, 10)
	workerHash := stableHash(tenant.ID+"\x00"+workerName, 10)
	versionHash := stableHash(versionID, 10)
	key := tenant.Slug + "-" + workerName + "-" + workerHash

	return CanonicalNames{
		TenantWorkerKey:      key,
		TenantHash:           tenantHash,
		VersionHash:          versionHash,
		TaskQueue:            "org-" + key,
		WorkerDeployment:     "org-" + key,
		WorkflowID:           "org-" + key + "-run-" + stableHash(opaqueRunID, 20),
		KubernetesDeployment: "org-" + boundedTenantWorkerKey(key, 48) + "-" + versionHash,
		ServiceAccount:       "org-" + boundedTenantWorkerKey(key, 59),
		NetworkPolicy:        "org-" + boundedTenantWorkerKey(key, 45) + "-np-" + versionHash,
	}, nil
}

func stableHash(value string, length int) string {
	sum := sha256.Sum256([]byte(value))
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:])
	return strings.ToLower(encoded[:length])
}

func boundedTenantWorkerKey(key string, max int) string {
	if len(key) <= max {
		return key
	}
	hash := key[len(key)-10:]
	readable := strings.Trim(key[:max-11], "-")
	return readable + "-" + hash
}

type WorkerVersionState string

const (
	WorkerVersionPending WorkerVersionState = "pending"
	WorkerVersionReady   WorkerVersionState = "ready"
	WorkerVersionFailed  WorkerVersionState = "failed"
)

type WorkerVersionRequest struct {
	WorkerName     string           `json:"workerName"`
	Description    string           `json:"description"`
	Image          string           `json:"image"`
	Version        string           `json:"version"`
	ManifestDigest string           `json:"manifestDigest,omitempty"`
	VersionConfig  json.RawMessage  `json:"versionConfig,omitempty"`
	Metadata       WorkerMetadata   `json:"metadata"`
	Runtime        RuntimeSpec      `json:"runtime"`
	Source         SourceProvenance `json:"source"`
}
type WorkerMetadata struct {
	ContractVersion        string             `json:"contractVersion,omitempty"`
	ProjectionEventVersion string             `json:"projectionEventVersion,omitempty"`
	DynamicNodeIDVersion   string             `json:"dynamicNodeIdVersion,omitempty"`
	SDK                    SDKContract        `json:"sdk,omitempty"`
	Workflows              []WorkflowContract `json:"workflows"`
	Activities             []ActivityContract `json:"activities"`
}
type SDKContract struct {
	ModuleVersion          string `json:"moduleVersion"`
	RuntimeProtocolVersion string `json:"runtimeProtocolVersion"`
}
type WorkflowContract struct {
	Name               string           `json:"name"`
	VersioningBehavior string           `json:"versioningBehavior"`
	ProjectionQuery    string           `json:"projectionQuery"`
	InputSchema        json.RawMessage  `json:"inputSchema,omitempty"`
	OutputSchema       json.RawMessage  `json:"outputSchema,omitempty"`
	NodeTemplates      []NodeTemplate   `json:"nodeTemplates,omitempty"`
	Actions            []ActionContract `json:"actions,omitempty"`
	RuntimeBounds      RuntimeBounds    `json:"runtimeBounds,omitempty"`
	Signals            []Operation      `json:"signals,omitempty"`
	Queries            []Operation      `json:"queries,omitempty"`
	Steps              []DAGStep        `json:"steps,omitempty"`
}
type ActionContract struct {
	Name               string          `json:"name"`
	Label              string          `json:"label"`
	NodeTemplateID     string          `json:"nodeTemplateId,omitempty"`
	RequiredPermission string          `json:"requiredPermission"`
	InputSchema        json.RawMessage `json:"inputSchema,omitempty"`
}
type Operation struct {
	Name        string          `json:"name"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}
type DAGStep struct {
	ID             string   `json:"id"`
	Label          string   `json:"label"`
	DependsOn      []string `json:"dependsOn,omitempty"`
	AllowedActions []string `json:"allowedActions,omitempty"`
}
type NodeTemplate struct {
	ID           string           `json:"id"`
	Label        string           `json:"label"`
	Type         string           `json:"type"`
	Cardinality  string           `json:"cardinality,omitempty"`
	InputSchema  json.RawMessage  `json:"inputSchema,omitempty"`
	OutputSchema json.RawMessage  `json:"outputSchema,omitempty"`
	Activity     *ActivityPolicy  `json:"activity,omitempty"`
	Actions      []ActionContract `json:"actions,omitempty"`
}
type RuntimeBounds struct {
	MaxInstancesPerFanOut int `json:"maxInstancesPerFanOut"`
	MaxRuntimeNodes       int `json:"maxRuntimeNodes"`
	MaxProjectionBytes    int `json:"maxProjectionBytes"`
}
type ActivityPolicy struct {
	SideEffect     string                    `json:"sideEffect"`
	Retry          DynamicRetryPolicy        `json:"retryPolicy"`
	Idempotency    *DynamicIdempotencyPolicy `json:"idempotency,omitempty"`
	Reconciliation string                    `json:"reconciliation,omitempty"`
	Compensation   string                    `json:"compensation,omitempty"`
}
type DynamicRetryPolicy struct {
	InitialInterval     time.Duration `json:"initialInterval"`
	BackoffCoefficient  float64       `json:"backoffCoefficient"`
	MaximumInterval     time.Duration `json:"maximumInterval"`
	MaximumAttempts     int32         `json:"maximumAttempts"`
	StartToCloseTimeout time.Duration `json:"startToCloseTimeout"`
}
type DynamicIdempotencyPolicy struct {
	BusinessKeyRequired bool   `json:"businessKeyRequired"`
	PropagationField    string `json:"propagationField,omitempty"`
}
type ActivityContract struct {
	Name                 string                  `json:"name"`
	InputSchema          json.RawMessage         `json:"inputSchema,omitempty"`
	OutputSchema         json.RawMessage         `json:"outputSchema,omitempty"`
	Policy               *ActivityPolicy         `json:"policy,omitempty"`
	Kind                 string                  `json:"kind"`
	IdempotencyKey       *IdempotencyKeyContract `json:"idempotencyKey,omitempty"`
	ReconciliationPolicy string                  `json:"reconciliationPolicy,omitempty"`
	RetryPolicy          RetryPolicy             `json:"retryPolicy"`
}

func (activity ActivityContract) MarshalJSON() ([]byte, error) {
	if activity.Policy != nil {
		return json.Marshal(struct {
			Name         string          `json:"name"`
			InputSchema  json.RawMessage `json:"inputSchema,omitempty"`
			OutputSchema json.RawMessage `json:"outputSchema,omitempty"`
			Policy       ActivityPolicy  `json:"policy"`
		}{Name: activity.Name, InputSchema: activity.InputSchema, OutputSchema: activity.OutputSchema, Policy: *activity.Policy})
	}
	type legacyActivity ActivityContract
	return json.Marshal(legacyActivity(activity))
}

type IdempotencyKeyContract struct {
	Field      string `json:"field"`
	Derivation string `json:"derivation"`
}
type RetryPolicy struct {
	MaximumAttempts int `json:"maximumAttempts"`
}
type RuntimeSpec struct {
	CPU            string         `json:"cpu"`
	Memory         string         `json:"memory"`
	ServiceAccount string         `json:"-"`
	Environment    []EnvReference `json:"environment,omitempty"`
}
type EnvReference struct {
	Name      string `json:"name"`
	Secret    string `json:"secret"`
	SecretKey string `json:"secretKey"`
}
type SourceProvenance struct {
	Repository  string `json:"repository"`
	Branch      string `json:"branch,omitempty"`
	Commit      string `json:"commit"`
	CIReference string `json:"ciReference"`
}
type WorkerVersionHealth struct {
	KubernetesReady bool `json:"kubernetesReady"`
	WorkerPolling   bool `json:"workerPolling"`
}
type Worker struct {
	TenantID       string    `json:"tenantId"`
	TenantSlug     string    `json:"tenantSlug"`
	Name           string    `json:"workerName"`
	CurrentVersion string    `json:"currentVersion,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type WorkerVersion struct {
	ID                       string              `json:"id"`
	TenantID                 string              `json:"tenantId"`
	TenantSlug               string              `json:"tenantSlug"`
	WorkerName               string              `json:"workerName"`
	Description              string              `json:"description"`
	Revision                 int64               `json:"revision"`
	Image                    string              `json:"image"`
	Version                  string              `json:"version"`
	ManifestDigest           string              `json:"manifestDigest,omitempty"`
	VersionConfig            json.RawMessage     `json:"versionConfig,omitempty"`
	Metadata                 WorkerMetadata      `json:"metadata"`
	Runtime                  RuntimeSpec         `json:"runtime"`
	Source                   SourceProvenance    `json:"source"`
	TaskQueue                string              `json:"-"`
	WorkerDeployment         string              `json:"-"`
	KubernetesDeployment     string              `json:"-"`
	KubernetesServiceAccount string              `json:"-"`
	KubernetesNetworkPolicy  string              `json:"-"`
	TenantHash               string              `json:"-"`
	VersionHash              string              `json:"-"`
	State                    WorkerVersionState  `json:"state"`
	Health                   WorkerVersionHealth `json:"health"`
	Current                  bool                `json:"current"`
	Actor                    string              `json:"actor"`
	CreatedAt                time.Time           `json:"createdAt"`
	UpdatedAt                time.Time           `json:"updatedAt"`
	Failure                  string              `json:"failure,omitempty"`
}
type Invocation struct {
	ID                 string          `json:"id"`
	TenantID           string          `json:"tenantId"`
	TenantSlug         string          `json:"tenantSlug"`
	WorkerName         string          `json:"workerName"`
	Workflow           string          `json:"workflow"`
	SelectedVersion    string          `json:"selectedVersion"`
	TaskQueue          string          `json:"-"`
	WorkerDeployment   string          `json:"-"`
	TemporalWorkflowID string          `json:"-"`
	TemporalRunID      string          `json:"-"`
	Input              json.RawMessage `json:"input"`
	IdempotencyKey     string          `json:"idempotencyKey,omitempty"`
	Actor              string          `json:"actor"`
	CreatedAt          time.Time       `json:"createdAt"`
}

type AuditRecord struct {
	ID                   string            `json:"id"`
	TenantID             string            `json:"tenantId"`
	TenantSlug           string            `json:"tenantSlug"`
	PrincipalID          string            `json:"principalId"`
	AuthenticationMethod string            `json:"authenticationMethod"`
	RequestID            string            `json:"requestId"`
	Action               string            `json:"action"`
	Permission           string            `json:"permission"`
	AuthorizationResult  string            `json:"authorizationResult"`
	Outcome              string            `json:"outcome"`
	TargetType           string            `json:"targetType"`
	TargetID             string            `json:"targetId,omitempty"`
	ErrorClass           string            `json:"errorClass,omitempty"`
	References           map[string]string `json:"references,omitempty"`
	CreatedAt            time.Time         `json:"createdAt"`
}

type ActionDeliveryState string

const (
	ActionDeliveryReserved  ActionDeliveryState = "reserved"
	ActionDeliveryDelivered ActionDeliveryState = "delivered"
	ActionDeliveryUnknown   ActionDeliveryState = "delivery-unknown"
	ActionDeliveryAccepted  ActionDeliveryState = "accepted-by-workflow"
	ActionDeliveryRejected  ActionDeliveryState = "rejected-by-workflow"
)

type ActionOperation struct {
	ID            string              `json:"id"`
	TenantID      string              `json:"tenantId"`
	RunID         string              `json:"runId"`
	RuntimeNodeID string              `json:"runtimeNodeId"`
	Action        string              `json:"action"`
	OperationID   string              `json:"operationId"`
	PayloadDigest string              `json:"payloadDigest"`
	State         ActionDeliveryState `json:"state"`
	PrincipalID   string              `json:"principalId"`
	RequestID     string              `json:"requestId"`
	CreatedAt     time.Time           `json:"createdAt"`
	UpdatedAt     time.Time           `json:"updatedAt"`
}
type WorkflowProjection struct {
	TenantID       string           `json:"tenantId"`
	TenantSlug     string           `json:"tenantSlug"`
	Steps          []StepProjection `json:"steps"`
	CurrentStep    string           `json:"currentStep"`
	Status         string           `json:"status"`
	BlockReason    string           `json:"blockReason,omitempty"`
	AllowedActions []string         `json:"allowedActions"`
}
type StepProjection struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Status string `json:"status"`
}

func ValidateWorkerVersion(req WorkerVersionRequest, allowlist []string) error {
	var problems []string
	if !namePattern.MatchString(req.WorkerName) || strings.Contains(req.WorkerName, ".") || !versionPattern.MatchString(req.Version) {
		problems = append(problems, "workerName and version must be safe names")
	}
	if err := ValidateDescription(req.Description); err != nil {
		problems = append(problems, err.Error())
	}
	if len(req.VersionConfig) != 0 {
		if len(req.VersionConfig) > 64<<10 {
			problems = append(problems, "versionConfig exceeds 64 KiB")
		} else {
			var config map[string]any
			if err := json.Unmarshal(req.VersionConfig, &config); err != nil || config == nil {
				problems = append(problems, "versionConfig must be a JSON object")
			} else {
				for _, forbidden := range []string{"scope", "tenantId", "tenantSlug", "workerName", "taskQueue", "workerDeployment", "workflowId", "temporalNamespace", "kubernetesNamespace"} {
					if _, exists := config[forbidden]; exists {
						problems = append(problems, "versionConfig must not override product or routing identity")
						break
					}
				}
			}
		}
	}
	if !digestPattern.MatchString(req.Image) {
		problems = append(problems, "image must use an immutable sha256 digest")
	} else if !registryAllowed(req.Image, allowlist) {
		problems = append(problems, "image registry is not allowlisted")
	}
	if len(req.Metadata.Workflows) == 0 {
		problems = append(problems, "metadata must declare at least one workflow")
	}
	dynamicContract := req.Metadata.ContractVersion != ""
	if dynamicContract {
		if req.Metadata.ContractVersion != OrgSDKContractVersion || req.Metadata.ProjectionEventVersion != OrgSDKProjectionEventVersion || req.Metadata.DynamicNodeIDVersion != OrgSDKDynamicNodeIDVersion {
			problems = append(problems, "Org SDK manifest versions are unsupported")
		}
		if strings.TrimSpace(req.Metadata.SDK.ModuleVersion) == "" || strings.TrimSpace(req.Metadata.SDK.RuntimeProtocolVersion) == "" {
			problems = append(problems, "Org SDK manifest identity is required")
		} else if req.Metadata.SDK.ModuleVersion != OrgSDKModuleVersion || req.Metadata.SDK.RuntimeProtocolVersion != OrgSDKRuntimeProtocolVersion {
			problems = append(problems, "unsupported SDK module or runtime protocol version")
		}
		if !manifestDigestPattern.MatchString(req.ManifestDigest) {
			problems = append(problems, "Org SDK manifest digest is required")
		} else if digest, err := workerMetadataDigest(req.Metadata); err != nil || digest != req.ManifestDigest {
			problems = append(problems, "Org SDK manifest digest does not match metadata")
		}
	}
	for _, w := range req.Metadata.Workflows {
		if strings.ToLower(w.VersioningBehavior) != "pinned" {
			problems = append(problems, fmt.Sprintf("workflow %q must declare pinned versioning behavior", w.Name))
		}
		if dynamicContract {
			if err := validateDynamicWorkflow(w); err != nil {
				problems = append(problems, fmt.Sprintf("workflow %q dynamic contract: %v", w.Name, err))
			}
		} else {
			if w.ProjectionQuery == "" || len(w.Steps) == 0 {
				problems = append(problems, fmt.Sprintf("workflow %q must declare a projection query and DAG steps", w.Name))
			}
			if err := validateDAG(w.Steps); err != nil {
				problems = append(problems, fmt.Sprintf("workflow %q DAG: %v", w.Name, err))
			}
		}
		actionNames := map[string]bool{}
		for _, action := range w.Actions {
			if !namePattern.MatchString(action.Name) || strings.TrimSpace(action.Label) == "" || !namePattern.MatchString(action.NodeTemplateID) {
				problems = append(problems, fmt.Sprintf("workflow %q action name, label, and node template are required", w.Name))
			}
			if !permissionPattern.MatchString(action.RequiredPermission) {
				problems = append(problems, fmt.Sprintf("workflow %q action %q permission is invalid", w.Name, action.Name))
			}
			if len(action.InputSchema) > 0 && !json.Valid(action.InputSchema) {
				problems = append(problems, fmt.Sprintf("workflow %q action %q schema is invalid", w.Name, action.Name))
			}
			if actionNames[action.Name] {
				problems = append(problems, fmt.Sprintf("workflow %q has duplicate action %q", w.Name, action.Name))
			}
			actionNames[action.Name] = true
		}
	}
	if dynamicContract {
		if err := validateDynamicActivities(req.Metadata); err != nil {
			problems = append(problems, err.Error())
		}
	}
	for _, a := range req.Metadata.Activities {
		if a.Kind != "write" {
			continue
		}
		hasKey := a.IdempotencyKey != nil && a.IdempotencyKey.Field != "" && a.IdempotencyKey.Derivation != ""
		hasReconciliation := strings.TrimSpace(a.ReconciliationPolicy) != ""
		if !hasKey && !hasReconciliation {
			problems = append(problems, fmt.Sprintf("write activity %q requires an idempotency key or reconciliation policy", a.Name))
		}
		if !hasKey && a.RetryPolicy.MaximumAttempts != 1 {
			problems = append(problems, fmt.Sprintf("write activity %q without idempotency must disable automatic retries", a.Name))
		}
	}
	if req.Runtime.CPU == "" || req.Runtime.Memory == "" {
		problems = append(problems, "runtime CPU and memory are required")
	}
	if !quantityPattern.MatchString(req.Runtime.CPU) || !quantityPattern.MatchString(req.Runtime.Memory) {
		problems = append(problems, "runtime resources contain unsafe values")
	}
	for _, ref := range req.Runtime.Environment {
		if !envNamePattern.MatchString(ref.Name) || !namePattern.MatchString(ref.Secret) || !secretKeyPattern.MatchString(ref.SecretKey) {
			problems = append(problems, "environment reference contains an unsafe name")
		}
	}
	if u, err := url.ParseRequestURI(req.Source.Repository); err != nil || u.Scheme == "" || u.Host == "" {
		problems = append(problems, "source repository must be an absolute URL")
	}
	if !commitPattern.MatchString(req.Source.Commit) {
		problems = append(problems, "source commit must be a commit hash")
	}
	if req.Source.CIReference == "" {
		problems = append(problems, "source CI reference is required")
	}
	if len(problems) != 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func validateDynamicActivities(metadata WorkerMetadata) error {
	declared := map[string]NodeTemplate{}
	for _, workflow := range metadata.Workflows {
		for _, template := range workflow.NodeTemplates {
			if template.Type != "activity" {
				continue
			}
			if _, duplicate := declared[template.ID]; duplicate {
				return fmt.Errorf("activity template %q is declared more than once", template.ID)
			}
			declared[template.ID] = template
		}
	}
	seen := map[string]bool{}
	for _, activity := range metadata.Activities {
		template, exists := declared[activity.Name]
		if !exists || seen[activity.Name] || activity.Policy == nil || template.Activity == nil {
			return fmt.Errorf("activity manifest %q is missing, duplicated, or undeclared", activity.Name)
		}
		seen[activity.Name] = true
		if string(activity.InputSchema) != string(template.InputSchema) || string(activity.OutputSchema) != string(template.OutputSchema) || !dynamicActivityPoliciesEqual(*activity.Policy, *template.Activity) {
			return fmt.Errorf("activity manifest %q does not match its node template", activity.Name)
		}
	}
	if len(seen) != len(declared) {
		return errors.New("activity manifest does not cover every activity node template")
	}
	return nil
}

func dynamicActivityPoliciesEqual(left, right ActivityPolicy) bool {
	if left.SideEffect != right.SideEffect || left.Retry != right.Retry || left.Reconciliation != right.Reconciliation || left.Compensation != right.Compensation {
		return false
	}
	if left.Idempotency == nil || right.Idempotency == nil {
		return left.Idempotency == nil && right.Idempotency == nil
	}
	return *left.Idempotency == *right.Idempotency
}

func workerMetadataDigest(metadata WorkerMetadata) (string, error) {
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func ValidateDescription(value string) error {
	value = strings.TrimSpace(value)
	length := len([]rune(value))
	if length < 1 || length > 2000 {
		return errors.New("description must contain 1 to 2000 Unicode code points")
	}
	for _, r := range value {
		if r == 0 || (r < 32 && r != '\n' && r != '\t' && r != '\r') || r == 127 {
			return errors.New("description contains an unsafe control character")
		}
	}
	return nil
}

func ValidateWorkerName(value string) error {
	if !namePattern.MatchString(value) || strings.Contains(value, ".") || len(value) > 48 {
		return errors.New("workerName must be a lower-case DNS label of at most 48 characters")
	}
	return nil
}

func registryAllowed(image string, allowlist []string) bool {
	registry := strings.SplitN(image, "/", 2)[0]
	for _, allowed := range allowlist {
		if strings.EqualFold(strings.TrimSpace(allowed), registry) {
			return true
		}
	}
	return false
}

func validateDynamicWorkflow(workflow WorkflowContract) error {
	var problems []string
	if !versionPattern.MatchString(workflow.Name) {
		problems = append(problems, "workflow name must be a stable identifier")
	}
	if workflow.ProjectionQuery != OrgSDKProjectionQuery {
		problems = append(problems, "projection query must use the reserved Org SDK query")
	}
	if len(workflow.InputSchema) > 0 && !json.Valid(workflow.InputSchema) || len(workflow.OutputSchema) > 0 && !json.Valid(workflow.OutputSchema) {
		problems = append(problems, "input and output schemas must be valid JSON")
	}
	if workflow.RuntimeBounds.MaxInstancesPerFanOut <= 0 || workflow.RuntimeBounds.MaxRuntimeNodes <= 0 || workflow.RuntimeBounds.MaxProjectionBytes <= 0 {
		problems = append(problems, "runtime bounds must be finite positive values")
	}
	if len(workflow.NodeTemplates) == 0 {
		problems = append(problems, "at least one node template is required")
	}
	templates := make(map[string]NodeTemplate, len(workflow.NodeTemplates))
	for _, template := range workflow.NodeTemplates {
		if !dynamicIDPattern.MatchString(template.ID) || strings.HasPrefix(template.ID, "org.sdk/") || strings.TrimSpace(template.Label) == "" {
			problems = append(problems, "node template ID and label are invalid")
		}
		if _, exists := templates[template.ID]; exists {
			problems = append(problems, fmt.Sprintf("duplicate node template %q", template.ID))
		}
		templates[template.ID] = template
		if template.Cardinality != "" && template.Cardinality != "singleton" && template.Cardinality != "repeated" {
			problems = append(problems, fmt.Sprintf("node template %q has invalid cardinality", template.ID))
		}
		switch template.Type {
		case "activity":
			if template.Activity == nil {
				problems = append(problems, fmt.Sprintf("activity template %q requires a policy", template.ID))
				continue
			}
			policy := template.Activity
			if policy.Retry.MaximumAttempts <= 0 || policy.Retry.StartToCloseTimeout <= 0 {
				problems = append(problems, fmt.Sprintf("activity template %q requires retry attempts and timeout", template.ID))
			}
			if policy.SideEffect != "none" && policy.SideEffect != "read" && policy.SideEffect != "write" {
				problems = append(problems, fmt.Sprintf("activity template %q has invalid side effect", template.ID))
			}
			if policy.SideEffect == "write" && policy.Idempotency == nil && strings.TrimSpace(policy.Reconciliation) == "" {
				problems = append(problems, fmt.Sprintf("write activity %q requires idempotency or reconciliation", template.ID))
			}
			if policy.SideEffect == "write" && policy.Idempotency == nil && policy.Retry.MaximumAttempts != 1 {
				problems = append(problems, fmt.Sprintf("write activity %q without idempotency must disable automatic retries", template.ID))
			}
		case "wait-for-action":
			if len(template.Actions) == 0 {
				problems = append(problems, fmt.Sprintf("wait template %q requires actions", template.ID))
			}
		case "semantic":
		default:
			problems = append(problems, fmt.Sprintf("node template %q has invalid type", template.ID))
		}
	}
	seenActions := map[string]bool{}
	for _, action := range workflow.Actions {
		if seenActions[action.Name] {
			problems = append(problems, fmt.Sprintf("duplicate action %q", action.Name))
		}
		seenActions[action.Name] = true
		template, exists := templates[action.NodeTemplateID]
		if !exists || template.Type != "wait-for-action" {
			problems = append(problems, fmt.Sprintf("action %q references an invalid wait template", action.Name))
			continue
		}
		declared := false
		for _, candidate := range template.Actions {
			if candidate.Name == action.Name && candidate.Label == action.Label && candidate.RequiredPermission == action.RequiredPermission {
				declared = true
				break
			}
		}
		if !declared {
			problems = append(problems, fmt.Sprintf("action %q does not match its node template", action.Name))
		}
	}
	if len(problems) != 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func validateDAG(steps []DAGStep) error {
	graph := make(map[string][]string, len(steps))
	for _, step := range steps {
		if step.ID == "" || step.Label == "" {
			return errors.New("step ID and label are required")
		}
		if _, exists := graph[step.ID]; exists {
			return fmt.Errorf("duplicate step %q", step.ID)
		}
		graph[step.ID] = step.DependsOn
	}
	for id, dependencies := range graph {
		for _, dependency := range dependencies {
			if _, exists := graph[dependency]; !exists {
				return fmt.Errorf("step %q depends on unknown step %q", id, dependency)
			}
		}
	}
	state := map[string]uint8{}
	var visit func(string) bool
	visit = func(id string) bool {
		if state[id] == 1 {
			return true
		}
		if state[id] == 2 {
			return false
		}
		state[id] = 1
		for _, dependency := range graph[id] {
			if visit(dependency) {
				return true
			}
		}
		state[id] = 2
		return false
	}
	for id := range graph {
		if visit(id) {
			return errors.New("cycle detected")
		}
	}
	return nil
}
func (m WorkerMetadata) Workflow(name string) (WorkflowContract, bool) {
	for _, w := range m.Workflows {
		if w.Name == name {
			return w, true
		}
	}
	return WorkflowContract{}, false
}
