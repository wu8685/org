package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wu8685/org/internal/domain"
	"github.com/wu8685/org/sdk/orgsdk"
)

type Config struct {
	RegistryAllowlist         []string
	TemporalWebBaseURL        string
	TemporalNamespace         string
	BootstrapTTL              time.Duration
	BootstrapReceiptGrace     time.Duration
	BootstrapVerifier         BootstrapWorkloadVerifier
	Now                       func() time.Time
	BootstrapEndpoint         string
	PublishOperationRetention time.Duration
}

var (
	ErrUnauthenticated     = errors.New("unauthenticated")
	ErrPermissionDenied    = errors.New("permission_denied")
	ErrNotFound            = errors.New("not_found")
	ErrTenantSuspended     = errors.New("tenant_suspended")
	ErrTenantQuotaExceeded = errors.New("tenant_quota_exceeded")
	ErrConflict            = errors.New("conflict")
)

const (
	PermissionWorkerDeploy        = "worker:deploy"
	PermissionWorkerCreate        = "worker:create"
	PermissionWorkerRead          = "worker:read"
	PermissionWorkerVersionUpdate = "worker:version:update"
	PermissionRunStart            = "run:start"
	PermissionRunRead             = "run:read"
	PermissionRunSignal           = "run:signal"
	PermissionRunQuery            = "run:query"
	PermissionRunCancel           = "run:cancel"
	PermissionAuditRead           = "audit:read"
	PermissionDiagnosticsRead     = "diagnostics:read"
	PermissionTenantAdmin         = "tenant:admin"
)

type AuthenticatedContext struct {
	PrincipalID          string
	TenantID             string
	TenantSlug           string
	AuthenticationMethod string
	RequestID            string
	Permissions          map[string]bool
}
type Cluster interface {
	Apply(context.Context, domain.WorkerVersion) error
	WaitReady(context.Context, domain.WorkerVersion) error
}
type BootstrapDeployment struct {
	Endpoint   string
	Credential string
	Generation string
	ExpiresAt  time.Time
}
type BootstrapCluster interface {
	ApplyBootstrap(context.Context, domain.WorkerVersion, BootstrapDeployment) error
}
type Executor interface {
	WaitForPoller(context.Context, domain.WorkerVersion) error
	Probe(context.Context, domain.WorkerVersion) (RuntimeIdentity, error)
	SetCurrent(context.Context, domain.WorkerVersion) error
	Start(context.Context, ExecutionStart) (string, error)
	Describe(context.Context, domain.Invocation) (ExecutionState, error)
	Query(context.Context, domain.Invocation, string, []byte) ([]byte, error)
	Signal(context.Context, domain.Invocation, string, []byte) error
	Cancel(context.Context, domain.Invocation) error
}
type RuntimeIdentity struct {
	ManifestDigest         string `json:"manifestDigest"`
	SDKModuleVersion       string `json:"sdkModuleVersion"`
	RuntimeProtocolVersion string `json:"runtimeProtocolVersion"`
	WorkerBuildID          string `json:"workerBuildId"`
}
type ExecutionStart struct {
	InvocationID, WorkflowID, Workflow       string
	Input                                    []byte
	TaskQueue, DeploymentName, PinnedVersion string
}
type ExecutionState struct{ Status, Result string }
type InvocationView struct {
	Invocation             domain.Invocation         `json:"invocation"`
	WorkerVersion          domain.WorkerVersion      `json:"workerVersion"`
	Execution              ExecutionState            `json:"execution"`
	Projection             domain.WorkflowProjection `json:"projection"`
	SemanticProjection     *orgsdk.Projection        `json:"semanticProjection,omitempty"`
	TemporalDiagnosticsURL string                    `json:"temporalDiagnosticsUrl,omitempty"`
}
type WorkerView struct {
	Worker   domain.Worker          `json:"worker"`
	Versions []domain.WorkerVersion `json:"versions"`
}
type StartRequest struct {
	WorkerName     string          `json:"workerName"`
	Workflow       string          `json:"workflow"`
	WorkerVersion  string          `json:"workerVersion,omitempty"`
	IdempotencyKey string          `json:"idempotencyKey,omitempty"`
	Input          json.RawMessage `json:"input"`
}
type CreateWorkerRequest struct {
	WorkerName string `json:"workerName"`
}
type Store interface {
	SaveTenant(domain.Tenant) error
	Tenant(string) (domain.Tenant, bool)
	TenantBySlug(string) (domain.Tenant, bool)
	SaveWorker(string, domain.Worker) error
	Worker(string, string) (domain.Worker, bool)
	Workers(string) []domain.Worker
	SaveWorkerVersion(string, domain.WorkerVersion) error
	WorkerVersions(string, string) []domain.WorkerVersion
	WorkerVersion(string, string, string) (domain.WorkerVersion, bool)
	UpdateWorkerVersionDescription(string, string, string, int64, string) (domain.WorkerVersion, error)
	SaveInvocation(string, domain.Invocation) error
	Invocation(string, string) (domain.Invocation, bool)
	Invocations(string) []domain.Invocation
	InvocationByIdempotency(string, string, string, string) (domain.Invocation, bool)
	SaveActionOperation(string, domain.ActionOperation) error
	ActionOperation(string, string, string, string, string) (domain.ActionOperation, bool)
	ActionOperations(string, string) []domain.ActionOperation
	ReservePublishOperation(domain.PublishOperation, time.Time) (domain.PublishOperation, bool, error)
	SavePublishOperation(string, domain.PublishOperation) error
	PublishOperation(string, string) (domain.PublishOperation, bool)
	AppendAudit(string, domain.AuditRecord) error
	Audits(string) []domain.AuditRecord
	AcquireQuotaLease(string, domain.QuotaLease) error
	ReleaseQuotaLease(string, string) error
	QuotaLeases(string) []domain.QuotaLease
	ReconcileQuotaLeases(string, map[string]bool) (int, error)
	SaveBootstrapCredential(domain.BootstrapCredential) error
	BootstrapCredential(string) (domain.BootstrapCredential, bool)
	BootstrapCredentials() []domain.BootstrapCredential
	CommitBootstrapAcceptance(string, domain.WorkerVersion, domain.BootstrapCredential, domain.AuditRecord) error
}

type ControlPlane struct {
	cfg       Config
	store     Store
	cluster   Cluster
	executor  Executor
	bootstrap *BootstrapRegistry
	mu        sync.Mutex
}

func New(cfg Config, store Store, cluster Cluster, executor Executor) *ControlPlane {
	cp := &ControlPlane{cfg: cfg, store: store, cluster: cluster, executor: executor}
	cp.bootstrap = NewBootstrapRegistry(store, BootstrapRegistryConfig{TTL: cfg.BootstrapTTL, ReceiptGrace: cfg.BootstrapReceiptGrace, Now: cfg.Now, Verifier: cfg.BootstrapVerifier})
	return cp
}

func (c *ControlPlane) IssueBootstrap(version domain.WorkerVersion, deploymentGeneration string) (BootstrapMaterial, error) {
	return c.bootstrap.Issue(version, deploymentGeneration)
}

func (c *ControlPlane) RegisterBootstrap(ctx context.Context, token string, evidence BootstrapWorkloadEvidence, registration domain.WorkerContractRegistration) (BootstrapRegistrationReceipt, domain.WorkerVersion, error) {
	receipt, err := c.bootstrap.Register(ctx, token, evidence, registration)
	if err != nil {
		return BootstrapRegistrationReceipt{}, domain.WorkerVersion{}, err
	}
	version, err := c.bootstrapVersion(receipt)
	return receipt, version, err
}

func (c *ControlPlane) bootstrapVersion(receipt BootstrapRegistrationReceipt) (domain.WorkerVersion, error) {
	for _, credential := range c.store.BootstrapCredentials() {
		if credential.ReceiptID != receipt.ID || credential.Binding.WorkerVersionID != receipt.WorkerVersionID {
			continue
		}
		version, ok := c.store.WorkerVersion(credential.Binding.TenantID, credential.Binding.WorkerName, credential.Binding.Version)
		if ok {
			return version, nil
		}
	}
	return domain.WorkerVersion{}, ErrNotFound
}

func (c *ControlPlane) PromoteBootstrap(ctx context.Context, receipt BootstrapRegistrationReceipt) (domain.WorkerVersion, error) {
	version, err := c.bootstrapVersion(receipt)
	if err != nil {
		return domain.WorkerVersion{}, err
	}
	if version.State == domain.WorkerVersionReady {
		return version, nil
	}
	for !version.Health.KubernetesReady {
		if version.State == domain.WorkerVersionFailed {
			return version, errors.New("candidate workload failed before promotion")
		}
		select {
		case <-ctx.Done():
			return version, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
		refreshed, refreshErr := c.bootstrapVersion(receipt)
		if refreshErr != nil {
			return domain.WorkerVersion{}, refreshErr
		}
		version = refreshed
	}
	if err := c.executor.WaitForPoller(ctx, version); err != nil {
		failed, cause := c.failWorkerVersion(version.TenantID, version, err)
		return failed, cause
	}
	version.Health.WorkerPolling = true
	identity, err := c.executor.Probe(ctx, version)
	if err != nil {
		failed, cause := c.failWorkerVersion(version.TenantID, version, err)
		return failed, cause
	}
	if err := validateRuntimeIdentity(version, identity); err != nil {
		failed, cause := c.failWorkerVersion(version.TenantID, version, err)
		return failed, cause
	}
	if err := c.executor.SetCurrent(ctx, version); err != nil {
		failed, cause := c.failWorkerVersion(version.TenantID, version, err)
		return failed, cause
	}
	c.mu.Lock()
	locked := true
	defer func() {
		if locked {
			c.mu.Unlock()
		}
	}()
	worker, ok := c.store.Worker(version.TenantID, version.WorkerName)
	if !ok {
		return domain.WorkerVersion{}, ErrNotFound
	}
	for _, old := range c.store.WorkerVersions(version.TenantID, version.WorkerName) {
		if old.ID != version.ID && old.Current {
			old.Current = false
			if err := c.store.SaveWorkerVersion(version.TenantID, old); err != nil {
				return domain.WorkerVersion{}, err
			}
		}
	}
	version.Current, version.State, version.UpdatedAt = true, domain.WorkerVersionReady, time.Now().UTC()
	if err := c.store.SaveWorkerVersion(version.TenantID, version); err != nil {
		return domain.WorkerVersion{}, err
	}
	worker.CurrentVersion, worker.UpdatedAt = version.Version, time.Now().UTC()
	if err := c.store.SaveWorker(version.TenantID, worker); err != nil {
		return domain.WorkerVersion{}, err
	}
	return version, nil
}

func (c *ControlPlane) CreateWorker(_ context.Context, auth AuthenticatedContext, req CreateWorkerRequest) (result domain.Worker, err error) {
	tenant, err := c.authorize(auth, PermissionWorkerCreate, "worker.create", "worker", req.WorkerName)
	if err != nil {
		return domain.Worker{}, err
	}
	defer func() {
		c.auditAllowed(auth, tenant, PermissionWorkerCreate, "worker.create", "worker", req.WorkerName, err, map[string]string{"workerName": req.WorkerName})
	}()
	if tenant.Status != domain.TenantActive {
		return domain.Worker{}, ErrTenantSuspended
	}
	if err := domain.ValidateWorkerName(req.WorkerName); err != nil {
		return domain.Worker{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.store.Worker(tenant.ID, req.WorkerName); exists {
		return domain.Worker{}, ErrConflict
	}
	now := time.Now().UTC()
	result = domain.Worker{TenantID: tenant.ID, TenantSlug: tenant.Slug, Name: req.WorkerName, CreatedAt: now, UpdatedAt: now}
	if err := c.store.SaveWorker(tenant.ID, result); err != nil {
		return domain.Worker{}, err
	}
	return result, nil
}

func (c *ControlPlane) GetWorker(_ context.Context, auth AuthenticatedContext, workerName string) (result WorkerView, err error) {
	tenant, err := c.authorize(auth, PermissionWorkerRead, "worker.read", "worker", workerName)
	if err != nil {
		return WorkerView{}, err
	}
	defer func() {
		c.auditAllowed(auth, tenant, PermissionWorkerRead, "worker.read", "worker", workerName, err, map[string]string{"workerName": workerName})
	}()
	worker, ok := c.store.Worker(tenant.ID, workerName)
	if !ok {
		return WorkerView{}, ErrNotFound
	}
	versions := c.store.WorkerVersions(tenant.ID, workerName)
	sort.Slice(versions, func(i, j int) bool { return versions[i].CreatedAt.Before(versions[j].CreatedAt) })
	return WorkerView{Worker: worker, Versions: versions}, nil
}

func (c *ControlPlane) PublishVersion(ctx context.Context, auth AuthenticatedContext, req domain.WorkerVersionRequest) (result domain.WorkerVersion, err error) {
	tenant, err := c.authorize(auth, PermissionWorkerDeploy, "worker.version.publish", "workerVersion", "")
	if err != nil {
		return domain.WorkerVersion{}, err
	}
	defer func() {
		c.auditAllowed(auth, tenant, PermissionWorkerDeploy, "worker.version.publish", "workerVersion", result.ID, err, workerVersionReferences(result))
	}()
	if tenant.Status != domain.TenantActive {
		return domain.WorkerVersion{}, ErrTenantSuspended
	}
	if err := domain.ValidateWorkerVersion(req, c.cfg.RegistryAllowlist); err != nil {
		return domain.WorkerVersion{}, err
	}
	c.mu.Lock()
	locked := true
	defer func() {
		if locked {
			c.mu.Unlock()
		}
	}()
	worker, ok := c.store.Worker(tenant.ID, req.WorkerName)
	if !ok {
		return domain.WorkerVersion{}, ErrNotFound
	}
	for _, existing := range c.store.WorkerVersions(tenant.ID, req.WorkerName) {
		if existing.Version == req.Version {
			return domain.WorkerVersion{}, ErrConflict
		}
	}
	id := newID("ver")
	names, err := domain.CanonicalNamesFor(tenant, req.WorkerName, id, "pending")
	if err != nil {
		return domain.WorkerVersion{}, err
	}
	runtime := req.Runtime
	runtime.ServiceAccount = names.ServiceAccount
	now := time.Now().UTC()
	d := domain.WorkerVersion{ID: id, TenantID: tenant.ID, TenantSlug: tenant.Slug, WorkerName: req.WorkerName, Description: strings.TrimSpace(req.Description), Revision: 1, Image: req.Image, Version: req.Version, ManifestDigest: req.ManifestDigest, VersionConfig: append(json.RawMessage(nil), req.VersionConfig...), Metadata: req.Metadata, Runtime: runtime, Source: req.Source, TaskQueue: names.TaskQueue, WorkerDeployment: names.WorkerDeployment, KubernetesDeployment: names.KubernetesDeployment, KubernetesServiceAccount: names.ServiceAccount, KubernetesNetworkPolicy: names.NetworkPolicy, TenantHash: names.TenantHash, VersionHash: names.VersionHash, State: domain.WorkerVersionPending, Actor: auth.PrincipalID, CreatedAt: now, UpdatedAt: now}
	result = d
	cpuMilli, err := parseCPU(runtime.CPU)
	if err != nil {
		return domain.WorkerVersion{}, err
	}
	memoryBytes, err := parseMemory(runtime.Memory)
	if err != nil {
		return domain.WorkerVersion{}, err
	}
	operationLeaseID := "deployment:" + d.ID
	if err := c.store.AcquireQuotaLease(tenant.ID, domain.QuotaLease{ID: operationLeaseID, TenantID: tenant.ID, Kind: domain.QuotaLeaseDeployment, ConcurrentDeployments: 1, CreatedAt: time.Now().UTC()}); err != nil {
		return domain.WorkerVersion{}, err
	}
	defer c.store.ReleaseQuotaLease(tenant.ID, operationLeaseID)
	releaseLeaseID := "release:" + d.ID
	if err := c.store.AcquireQuotaLease(tenant.ID, domain.QuotaLease{ID: releaseLeaseID, TenantID: tenant.ID, Kind: domain.QuotaLeaseRelease, ReservedCPUMilli: cpuMilli, ReservedMemoryBytes: memoryBytes, ActiveWorkerPods: 1, ActiveReleases: 1, CreatedAt: time.Now().UTC()}); err != nil {
		return domain.WorkerVersion{}, err
	}
	defer func() {
		if err != nil {
			_ = c.store.ReleaseQuotaLease(tenant.ID, releaseLeaseID)
		}
	}()
	if err := c.store.SaveWorkerVersion(tenant.ID, d); err != nil {
		return domain.WorkerVersion{}, err
	}
	var applyErr error
	bootstrapPublish := d.ManifestDigest == "" && len(d.Metadata.Workflows) == 0
	if bootstrapPublish {
		bootstrapCluster, ok := c.cluster.(BootstrapCluster)
		if !ok || strings.TrimSpace(c.cfg.BootstrapEndpoint) == "" {
			return c.failWorkerVersion(tenant.ID, d, errors.New("bootstrap-capable cluster and endpoint are required"))
		}
		generation := newID("generation")
		material, issueErr := c.bootstrap.Issue(d, generation)
		if issueErr != nil {
			return c.failWorkerVersion(tenant.ID, d, issueErr)
		}
		c.mu.Unlock()
		locked = false
		applyErr = bootstrapCluster.ApplyBootstrap(ctx, d, BootstrapDeployment{Endpoint: c.cfg.BootstrapEndpoint, Credential: material.Token, Generation: generation, ExpiresAt: material.ExpiresAt})
	} else {
		c.mu.Unlock()
		locked = false
		applyErr = c.cluster.Apply(ctx, d)
	}
	if applyErr != nil {
		return c.failWorkerVersion(tenant.ID, d, applyErr)
	}
	if err := c.cluster.WaitReady(ctx, d); err != nil {
		return c.failWorkerVersion(tenant.ID, d, err)
	}
	if bootstrapPublish {
		latest, ok := c.store.WorkerVersion(tenant.ID, d.WorkerName, d.Version)
		if !ok || latest.ID != d.ID {
			return domain.WorkerVersion{}, ErrNotFound
		}
		latest.Health.KubernetesReady = true
		if latest.RegistrationStatus == "" {
			latest.RegistrationStatus = domain.BootstrapRegistrationAwaiting
		}
		latest.UpdatedAt = time.Now().UTC()
		if err := c.store.SaveWorkerVersion(tenant.ID, latest); err != nil {
			return domain.WorkerVersion{}, err
		}
		result = latest
		return latest, nil
	}
	d.Health.KubernetesReady = true
	if err := c.executor.WaitForPoller(ctx, d); err != nil {
		return c.failWorkerVersion(tenant.ID, d, err)
	}
	d.Health.WorkerPolling = true
	if d.Metadata.ContractVersion != "" {
		identity, probeErr := c.executor.Probe(ctx, d)
		if probeErr != nil {
			return c.failWorkerVersion(tenant.ID, d, probeErr)
		}
		if probeErr := validateRuntimeIdentity(d, identity); probeErr != nil {
			return c.failWorkerVersion(tenant.ID, d, probeErr)
		}
	}
	if err := c.executor.SetCurrent(ctx, d); err != nil {
		return c.failWorkerVersion(tenant.ID, d, err)
	}
	for _, old := range c.store.WorkerVersions(tenant.ID, req.WorkerName) {
		if old.ID != d.ID && old.Current {
			old.Current = false
			if err := c.store.SaveWorkerVersion(tenant.ID, old); err != nil {
				return domain.WorkerVersion{}, err
			}
		}
	}
	d.Current, d.State = true, domain.WorkerVersionReady
	if err := c.store.SaveWorkerVersion(tenant.ID, d); err != nil {
		return domain.WorkerVersion{}, err
	}
	worker.CurrentVersion, worker.UpdatedAt = d.Version, time.Now().UTC()
	if err := c.store.SaveWorker(tenant.ID, worker); err != nil {
		return domain.WorkerVersion{}, err
	}
	result = d
	return d, nil
}

func validateRuntimeIdentity(version domain.WorkerVersion, identity RuntimeIdentity) error {
	if identity.ManifestDigest != version.ManifestDigest {
		return errors.New("Worker runtime manifest digest does not match the registered manifest digest")
	}
	if identity.SDKModuleVersion != version.Metadata.SDK.ModuleVersion {
		return errors.New("Worker runtime SDK module version does not match the registered manifest")
	}
	if identity.RuntimeProtocolVersion != version.Metadata.SDK.RuntimeProtocolVersion {
		return errors.New("Worker runtime protocol version does not match the registered manifest")
	}
	if identity.WorkerBuildID != version.Version {
		return errors.New("Worker runtime Build ID does not match the registered Worker version")
	}
	return nil
}

func (c *ControlPlane) failWorkerVersion(tenantID string, d domain.WorkerVersion, cause error) (domain.WorkerVersion, error) {
	d.State, d.Failure = domain.WorkerVersionFailed, cause.Error()
	_ = c.store.SaveWorkerVersion(tenantID, d)
	return d, cause
}

func (c *ControlPlane) Start(ctx context.Context, auth AuthenticatedContext, req StartRequest) (result domain.Invocation, err error) {
	tenant, err := c.authorize(auth, PermissionRunStart, "run.start", "invocation", "")
	if err != nil {
		return domain.Invocation{}, err
	}
	defer func() {
		c.auditAllowed(auth, tenant, PermissionRunStart, "run.start", "invocation", result.ID, err, invocationReferences(result))
	}()
	if tenant.Status != domain.TenantActive {
		return domain.Invocation{}, ErrTenantSuspended
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if req.IdempotencyKey != "" {
		if existing, ok := c.store.InvocationByIdempotency(tenant.ID, req.WorkerName, req.Workflow, req.IdempotencyKey); ok {
			result = existing
			return existing, nil
		}
	}
	worker, ok := c.store.Worker(tenant.ID, req.WorkerName)
	if !ok {
		return domain.Invocation{}, ErrNotFound
	}
	var selected *domain.WorkerVersion
	for _, d := range c.store.WorkerVersions(tenant.ID, req.WorkerName) {
		if d.State != domain.WorkerVersionReady {
			continue
		}
		if req.WorkerVersion != "" && d.Version == req.WorkerVersion {
			copy := d
			selected = &copy
			break
		}
		if req.WorkerVersion == "" && d.Version == worker.CurrentVersion {
			copy := d
			selected = &copy
		}
	}
	if selected == nil {
		return domain.Invocation{}, errors.New("selected Worker version is not ready")
	}
	if _, ok := selected.Metadata.Workflow(req.Workflow); !ok {
		return domain.Invocation{}, fmt.Errorf("workflow %q is not declared", req.Workflow)
	}
	invocationID := newID("inv")
	if req.IdempotencyKey != "" {
		sum := sha256.Sum256([]byte(tenant.ID + "\x00" + req.WorkerName + "\x00" + req.Workflow + "\x00" + req.IdempotencyKey))
		invocationID = "inv-" + hex.EncodeToString(sum[:16])
	}
	names, err := domain.CanonicalNamesFor(tenant, req.WorkerName, selected.ID, invocationID)
	if err != nil {
		return domain.Invocation{}, err
	}
	start := ExecutionStart{InvocationID: invocationID, WorkflowID: names.WorkflowID, Workflow: req.Workflow, Input: req.Input, TaskQueue: selected.TaskQueue, DeploymentName: selected.WorkerDeployment}
	if req.WorkerVersion != "" {
		start.PinnedVersion = selected.Version
	}
	runLeaseID := "run:" + invocationID
	if err := c.store.AcquireQuotaLease(tenant.ID, domain.QuotaLease{ID: runLeaseID, TenantID: tenant.ID, Kind: domain.QuotaLeaseRun, ConcurrentRuns: 1, CreatedAt: time.Now().UTC()}); err != nil {
		return domain.Invocation{}, err
	}
	defer func() {
		if err != nil {
			_ = c.store.ReleaseQuotaLease(tenant.ID, runLeaseID)
		}
	}()
	runID, err := c.executor.Start(ctx, start)
	if err != nil {
		return domain.Invocation{}, err
	}
	inv := domain.Invocation{ID: invocationID, TenantID: tenant.ID, TenantSlug: tenant.Slug, WorkerName: req.WorkerName, Workflow: req.Workflow, SelectedVersion: selected.Version, TaskQueue: selected.TaskQueue, WorkerDeployment: selected.WorkerDeployment, TemporalWorkflowID: names.WorkflowID, TemporalRunID: runID, Input: req.Input, IdempotencyKey: req.IdempotencyKey, Actor: auth.PrincipalID, CreatedAt: time.Now().UTC()}
	if err := c.store.SaveInvocation(tenant.ID, inv); err != nil {
		return domain.Invocation{}, err
	}
	result = inv
	return inv, nil
}

func (c *ControlPlane) GetInvocation(ctx context.Context, auth AuthenticatedContext, id string) (result InvocationView, err error) {
	tenant, err := c.authorize(auth, PermissionRunRead, "run.read", "invocation", id)
	if err != nil {
		return InvocationView{}, err
	}
	defer func() {
		c.auditAllowed(auth, tenant, PermissionRunRead, "run.read", "invocation", id, err, invocationReferences(result.Invocation))
	}()
	inv, ok := c.store.Invocation(tenant.ID, id)
	if !ok {
		return InvocationView{}, ErrNotFound
	}
	version, contract, err := c.invocationContract(inv)
	if err != nil {
		return InvocationView{}, err
	}
	state, err := c.executor.Describe(ctx, inv)
	if err != nil {
		return InvocationView{}, err
	}
	if status := strings.ToLower(state.Status); status != "running" && status != "pending" {
		_ = c.store.ReleaseQuotaLease(tenant.ID, "run:"+inv.ID)
	}
	projectionJSON, err := c.executor.Query(ctx, inv, contract.ProjectionQuery, nil)
	if err != nil {
		return InvocationView{}, err
	}
	view := InvocationView{Invocation: inv, WorkerVersion: version, Execution: state}
	if version.Metadata.ContractVersion != "" {
		var projection orgsdk.Projection
		if err := json.Unmarshal(projectionJSON, &projection); err != nil {
			return InvocationView{}, fmt.Errorf("invalid Worker projection: %w", err)
		}
		if err := validateDynamicProjection(projectionJSON, projection, inv, contract); err != nil {
			return InvocationView{}, fmt.Errorf("invalid Worker projection: %w", err)
		}
		view.SemanticProjection = &projection
	} else {
		var projection domain.WorkflowProjection
		if err := json.Unmarshal(projectionJSON, &projection); err != nil {
			return InvocationView{}, fmt.Errorf("invalid Worker projection: %w", err)
		}
		projection.TenantID, projection.TenantSlug = tenant.ID, tenant.Slug
		view.Projection = projection
	}
	if auth.Permissions[PermissionDiagnosticsRead] && c.cfg.TemporalWebBaseURL != "" && c.cfg.TemporalNamespace != "" {
		view.TemporalDiagnosticsURL = strings.TrimRight(c.cfg.TemporalWebBaseURL, "/") + "/namespaces/" + url.PathEscape(c.cfg.TemporalNamespace) + "/workflows/" + url.PathEscape(inv.TemporalWorkflowID) + "/" + url.PathEscape(inv.TemporalRunID)
	}
	result = view
	return view, nil
}

var runtimeNodeHashPattern = regexp.MustCompile(`^[a-z2-7]{16}$`)

func validateDynamicProjection(encoded []byte, projection orgsdk.Projection, invocation domain.Invocation, contract domain.WorkflowContract) error {
	if projection.ContractVersion != domain.OrgSDKContractVersion || projection.WorkflowName != invocation.Workflow || projection.WorkerVersion != invocation.SelectedVersion || projection.Revision == 0 {
		return errors.New("projection identity or revision does not match the invocation")
	}
	if len(encoded) > contract.RuntimeBounds.MaxProjectionBytes || len(projection.Nodes) > contract.RuntimeBounds.MaxRuntimeNodes {
		return errors.New("projection exceeds declared runtime bounds")
	}
	templates := make(map[string]domain.NodeTemplate, len(contract.NodeTemplates))
	for _, template := range contract.NodeTemplates {
		templates[template.ID] = template
	}
	nodes := make(map[string]orgsdk.NodeProjection, len(projection.Nodes))
	active := make(map[string]bool)
	anyFailed := false
	allTerminal := len(projection.Nodes) > 0
	for _, node := range projection.Nodes {
		template, exists := templates[node.TemplateID]
		if !exists {
			return fmt.Errorf("node %q references undeclared template %q", node.RuntimeNodeID, node.TemplateID)
		}
		if _, duplicate := nodes[node.RuntimeNodeID]; node.RuntimeNodeID == "" || duplicate {
			return fmt.Errorf("runtime node ID %q is missing or duplicated", node.RuntimeNodeID)
		}
		prefix := strings.NewReplacer("/", "-", ".", "-", "_", "-").Replace(node.TemplateID) + "-"
		hash := strings.TrimPrefix(node.RuntimeNodeID, prefix)
		if hash == node.RuntimeNodeID || !runtimeNodeHashPattern.MatchString(hash) {
			return fmt.Errorf("runtime node ID %q does not match the declared derivation version", node.RuntimeNodeID)
		}
		if node.Label != template.Label {
			return fmt.Errorf("node %q label does not match its template", node.RuntimeNodeID)
		}
		switch node.Status {
		case orgsdk.NodeStatusPending:
			allTerminal = false
		case orgsdk.NodeStatusRunning, orgsdk.NodeStatusWaitingForUser:
			active[node.RuntimeNodeID] = true
			allTerminal = false
		case orgsdk.NodeStatusCompleted, orgsdk.NodeStatusCanceled, orgsdk.NodeStatusSkipped:
		case orgsdk.NodeStatusFailed, orgsdk.NodeStatusTimedOut:
			anyFailed = true
		default:
			return fmt.Errorf("node %q has invalid status %q", node.RuntimeNodeID, node.Status)
		}
		nodes[node.RuntimeNodeID] = node
	}
	for _, node := range projection.Nodes {
		seen := map[string]bool{}
		for _, dependency := range node.Dependencies {
			if dependency == node.RuntimeNodeID || seen[dependency] {
				return fmt.Errorf("node %q has an invalid dependency", node.RuntimeNodeID)
			}
			if _, exists := nodes[dependency]; !exists {
				return fmt.Errorf("node %q depends on unknown node %q", node.RuntimeNodeID, dependency)
			}
			seen[dependency] = true
		}
	}
	if dynamicProjectionHasCycle(nodes) {
		return errors.New("runtime graph contains a cycle")
	}
	current := make(map[string]bool, len(projection.CurrentNodeIDs))
	for _, id := range projection.CurrentNodeIDs {
		if current[id] || !active[id] {
			return fmt.Errorf("current node %q is duplicated or not active", id)
		}
		current[id] = true
	}
	if len(current) != len(active) {
		return errors.New("current node set does not match active nodes")
	}
	for id := range active {
		if !current[id] {
			return errors.New("current node set does not match active nodes")
		}
	}
	wantStatus := "running"
	if anyFailed {
		wantStatus = "failed"
	} else if allTerminal {
		wantStatus = "completed"
	}
	if projection.Status != wantStatus {
		return fmt.Errorf("run status %q does not match node states", projection.Status)
	}
	seenActions := map[string]bool{}
	for _, offered := range projection.AllowedActions {
		node, exists := nodes[offered.RuntimeNodeID]
		if !exists || node.Status != orgsdk.NodeStatusWaitingForUser {
			return fmt.Errorf("action %q is offered by a node that is not waiting", offered.Name)
		}
		key := offered.RuntimeNodeID + "\x00" + offered.Name
		if seenActions[key] {
			return fmt.Errorf("action %q is offered more than once", offered.Name)
		}
		seenActions[key] = true
		declared := false
		for _, action := range contract.Actions {
			if action.NodeTemplateID == node.TemplateID && action.Name == offered.Name && action.Label == offered.Label {
				declared = true
				break
			}
		}
		if !declared {
			return fmt.Errorf("action %q is not declared for node template %q", offered.Name, node.TemplateID)
		}
	}
	return nil
}

func dynamicProjectionHasCycle(nodes map[string]orgsdk.NodeProjection) bool {
	state := make(map[string]uint8, len(nodes))
	var visit func(string) bool
	visit = func(id string) bool {
		if state[id] == 1 {
			return true
		}
		if state[id] == 2 {
			return false
		}
		state[id] = 1
		for _, dependency := range nodes[id].Dependencies {
			if visit(dependency) {
				return true
			}
		}
		state[id] = 2
		return false
	}
	for id := range nodes {
		if visit(id) {
			return true
		}
	}
	return false
}

func (c *ControlPlane) Signal(ctx context.Context, auth AuthenticatedContext, id, name string, input []byte) (err error) {
	tenant, err := c.authorize(auth, PermissionRunSignal, "run.signal", "invocation", id)
	if err != nil {
		return err
	}
	defer func() { c.auditAllowed(auth, tenant, PermissionRunSignal, "run.signal", "invocation", id, err, nil) }()
	if tenant.Status != domain.TenantActive {
		return ErrTenantSuspended
	}
	inv, ok := c.store.Invocation(tenant.ID, id)
	if !ok {
		return ErrNotFound
	}
	_, contract, err := c.invocationContract(inv)
	if err != nil {
		return err
	}
	if !declaredOperation(contract.Signals, name) {
		return fmt.Errorf("signal %q is not declared", name)
	}
	return c.executor.Signal(ctx, inv, name, input)
}

func (c *ControlPlane) Query(ctx context.Context, auth AuthenticatedContext, id, name string, input []byte) (result []byte, err error) {
	tenant, err := c.authorize(auth, PermissionRunQuery, "run.query", "invocation", id)
	if err != nil {
		return nil, err
	}
	defer func() { c.auditAllowed(auth, tenant, PermissionRunQuery, "run.query", "invocation", id, err, nil) }()
	inv, ok := c.store.Invocation(tenant.ID, id)
	if !ok {
		return nil, ErrNotFound
	}
	_, contract, err := c.invocationContract(inv)
	if err != nil {
		return nil, err
	}
	if !declaredOperation(contract.Queries, name) {
		return nil, fmt.Errorf("query %q is not declared", name)
	}
	return c.executor.Query(ctx, inv, name, input)
}

func (c *ControlPlane) Cancel(ctx context.Context, auth AuthenticatedContext, id string) (err error) {
	tenant, err := c.authorize(auth, PermissionRunCancel, "run.cancel", "invocation", id)
	if err != nil {
		return err
	}
	defer func() { c.auditAllowed(auth, tenant, PermissionRunCancel, "run.cancel", "invocation", id, err, nil) }()
	if tenant.Status != domain.TenantActive && !auth.Permissions[PermissionTenantAdmin] {
		return ErrTenantSuspended
	}
	inv, ok := c.store.Invocation(tenant.ID, id)
	if !ok {
		return ErrNotFound
	}
	if err := c.executor.Cancel(ctx, inv); err != nil {
		return err
	}
	return c.store.ReleaseQuotaLease(tenant.ID, "run:"+inv.ID)
}

func (c *ControlPlane) UpdateWorkerVersionDescription(_ context.Context, auth AuthenticatedContext, workerName, version string, expectedRevision int64, description string) (result domain.WorkerVersion, err error) {
	tenant, err := c.authorize(auth, PermissionWorkerVersionUpdate, "worker.version.description.update", "workerVersion", workerName+"@"+version)
	if err != nil {
		return domain.WorkerVersion{}, err
	}
	previous, _ := c.store.WorkerVersion(tenant.ID, workerName, version)
	defer func() {
		refs := map[string]string{"workerName": workerName, "version": version}
		if previous.Description != "" {
			sum := sha256.Sum256([]byte(previous.Description))
			refs["oldDescriptionDigest"] = hex.EncodeToString(sum[:])
			refs["oldDescriptionLength"] = fmt.Sprint(len([]rune(previous.Description)))
		}
		if result.Description != "" {
			sum := sha256.Sum256([]byte(result.Description))
			refs["descriptionDigest"] = hex.EncodeToString(sum[:])
			refs["descriptionLength"] = fmt.Sprint(len([]rune(result.Description)))
			refs["revision"] = fmt.Sprint(result.Revision)
		}
		c.auditAllowed(auth, tenant, PermissionWorkerVersionUpdate, "worker.version.description.update", "workerVersion", workerName+"@"+version, err, refs)
	}()
	if tenant.Status != domain.TenantActive {
		return domain.WorkerVersion{}, ErrTenantSuspended
	}
	if err := domain.ValidateDescription(description); err != nil {
		return domain.WorkerVersion{}, err
	}
	return c.store.UpdateWorkerVersionDescription(tenant.ID, workerName, version, expectedRevision, strings.TrimSpace(description))
}

func (c *ControlPlane) ListAudits(_ context.Context, auth AuthenticatedContext) (result []domain.AuditRecord, err error) {
	tenant, err := c.authorize(auth, PermissionAuditRead, "audit.read", "audit", "")
	if err != nil {
		return nil, err
	}
	defer func() { c.auditAllowed(auth, tenant, PermissionAuditRead, "audit.read", "audit", "", err, nil) }()
	return c.store.Audits(tenant.ID), nil
}

func (c *ControlPlane) ReconcileQuota(_ context.Context, auth AuthenticatedContext, activeLeaseIDs map[string]bool) (removed int, err error) {
	tenant, err := c.authorize(auth, PermissionTenantAdmin, "tenant.quota.reconcile", "tenant", auth.TenantID)
	if err != nil {
		return 0, err
	}
	defer func() {
		c.auditAllowed(auth, tenant, PermissionTenantAdmin, "tenant.quota.reconcile", "tenant", tenant.ID, err, map[string]string{"removedLeases": fmt.Sprint(removed)})
	}()
	return c.store.ReconcileQuotaLeases(tenant.ID, activeLeaseIDs)
}

func (c *ControlPlane) invocationContract(inv domain.Invocation) (domain.WorkerVersion, domain.WorkflowContract, error) {
	for _, d := range c.store.WorkerVersions(inv.TenantID, inv.WorkerName) {
		if d.Version == inv.SelectedVersion {
			if contract, ok := d.Metadata.Workflow(inv.Workflow); ok {
				return d, contract, nil
			}
		}
	}
	return domain.WorkerVersion{}, domain.WorkflowContract{}, errors.New("invocation Worker contract not found")
}

func (c *ControlPlane) authorize(auth AuthenticatedContext, permission, action, targetType, targetID string) (domain.Tenant, error) {
	if auth.PrincipalID == "" || auth.TenantID == "" || auth.AuthenticationMethod == "" {
		return domain.Tenant{}, ErrUnauthenticated
	}
	tenant, ok := c.store.Tenant(auth.TenantID)
	if !ok || tenant.Slug != auth.TenantSlug || !auth.Permissions[permission] {
		c.audit(auth, tenant, permission, action, targetType, targetID, "denied", "denied", "permission_denied", nil)
		return domain.Tenant{}, ErrPermissionDenied
	}
	return tenant, nil
}

func (c *ControlPlane) auditAllowed(auth AuthenticatedContext, tenant domain.Tenant, permission, action, targetType, targetID string, operationErr error, references map[string]string) {
	outcome, errorClass := "success", ""
	if operationErr != nil {
		outcome, errorClass = "failure", classifyError(operationErr)
	}
	c.audit(auth, tenant, permission, action, targetType, targetID, "allowed", outcome, errorClass, references)
}

func (c *ControlPlane) audit(auth AuthenticatedContext, tenant domain.Tenant, permission, action, targetType, targetID, authorizationResult, outcome, errorClass string, references map[string]string) {
	tenantID, tenantSlug := tenant.ID, tenant.Slug
	if tenantID == "" {
		tenantID, tenantSlug = auth.TenantID, auth.TenantSlug
	}
	if tenantID == "" {
		return
	}
	_ = c.store.AppendAudit(tenantID, domain.AuditRecord{
		ID: newID("aud"), TenantID: tenantID, TenantSlug: tenantSlug, PrincipalID: auth.PrincipalID,
		AuthenticationMethod: auth.AuthenticationMethod, RequestID: auth.RequestID, Action: action,
		Permission: permission, AuthorizationResult: authorizationResult, Outcome: outcome,
		TargetType: targetType, TargetID: targetID, ErrorClass: errorClass, References: references, CreatedAt: time.Now().UTC(),
	})
}

func classifyError(err error) string {
	switch {
	case errors.Is(err, ErrPermissionDenied):
		return "permission_denied"
	case errors.Is(err, ErrTenantSuspended):
		return "tenant_suspended"
	case errors.Is(err, ErrTenantQuotaExceeded):
		return "tenant_quota_exceeded"
	case errors.Is(err, ErrNotFound):
		return "not_found"
	case errors.Is(err, ErrConflict):
		return "conflict"
	default:
		return "operation_failed"
	}
}

func workerVersionReferences(d domain.WorkerVersion) map[string]string {
	if d.ID == "" {
		return nil
	}
	refs := map[string]string{
		"workerName": d.WorkerName, "version": d.Version, "imageDigest": d.Image,
		"sourceRepository": d.Source.Repository, "sourceCommit": d.Source.Commit, "sourceCIReference": d.Source.CIReference,
		"taskQueue": d.TaskQueue, "workerDeployment": d.WorkerDeployment, "kubernetesDeployment": d.KubernetesDeployment,
	}
	if d.Description != "" {
		sum := sha256.Sum256([]byte(d.Description))
		refs["descriptionDigest"] = hex.EncodeToString(sum[:])
		refs["descriptionLength"] = fmt.Sprint(len([]rune(d.Description)))
		refs["revision"] = fmt.Sprint(d.Revision)
	}
	return refs
}

func invocationReferences(inv domain.Invocation) map[string]string {
	if inv.ID == "" {
		return nil
	}
	return map[string]string{
		"workerName": inv.WorkerName, "workflow": inv.Workflow, "taskQueue": inv.TaskQueue,
		"workerDeployment": inv.WorkerDeployment, "temporalWorkflowId": inv.TemporalWorkflowID,
	}
}

func DecodeStartRequest(reader io.Reader) (StartRequest, error) {
	var request StartRequest
	if err := decodeStrictRequest(reader, &request); err != nil {
		return StartRequest{}, err
	}
	return request, nil
}

func DecodeWorkerVersionRequest(reader io.Reader) (domain.WorkerVersionRequest, error) {
	var request domain.WorkerVersionRequest
	if err := decodeStrictRequest(reader, &request); err != nil {
		return domain.WorkerVersionRequest{}, err
	}
	return request, nil
}

func decodeStrictRequest(reader io.Reader, target any) error {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request must contain one JSON object")
		}
		return err
	}
	return nil
}

func declaredOperation(operations []domain.Operation, name string) bool {
	for _, operation := range operations {
		if operation.Name == name {
			return true
		}
	}
	return false
}

func newID(prefix string) string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return prefix + "-" + hex.EncodeToString(b)
}

type MemoryStore struct {
	mu                   sync.RWMutex
	tenants              map[string]domain.Tenant
	workers              map[string]domain.Worker
	versions             map[string]domain.WorkerVersion
	invocations          map[string]domain.Invocation
	audits               map[string][]domain.AuditRecord
	quotaLeases          map[string]domain.QuotaLease
	actionOperations     map[string]domain.ActionOperation
	publishOperations    map[string]domain.PublishOperation
	bootstrapCredentials map[string]domain.BootstrapCredential
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{tenants: map[string]domain.Tenant{}, workers: map[string]domain.Worker{}, versions: map[string]domain.WorkerVersion{}, invocations: map[string]domain.Invocation{}, audits: map[string][]domain.AuditRecord{}, quotaLeases: map[string]domain.QuotaLease{}, actionOperations: map[string]domain.ActionOperation{}, publishOperations: map[string]domain.PublishOperation{}, bootstrapCredentials: map[string]domain.BootstrapCredential{}}
}

func (s *MemoryStore) SaveBootstrapCredential(credential domain.BootstrapCredential) error {
	if credential.TokenHash == "" || credential.Binding.TenantID == "" {
		return errors.New("bootstrap credential binding is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bootstrapCredentials[credential.TokenHash] = credential
	return nil
}
func (s *MemoryStore) BootstrapCredential(tokenHash string) (domain.BootstrapCredential, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	credential, ok := s.bootstrapCredentials[tokenHash]
	return credential, ok
}
func (s *MemoryStore) BootstrapCredentials() []domain.BootstrapCredential {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.BootstrapCredential, 0, len(s.bootstrapCredentials))
	for _, credential := range s.bootstrapCredentials {
		out = append(out, credential)
	}
	return out
}
func (s *MemoryStore) SaveTenant(tenant domain.Tenant) error {
	if err := domain.ValidateTenant(tenant); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, existing := range s.tenants {
		if existing.Slug == tenant.Slug && id != tenant.ID {
			return errors.New("tenant slug already exists")
		}
		if id == tenant.ID && existing.Slug != tenant.Slug {
			return errors.New("tenant slug is immutable")
		}
	}
	s.tenants[tenant.ID] = tenant
	return nil
}
func (s *MemoryStore) Tenant(id string) (domain.Tenant, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tenant, ok := s.tenants[id]
	return tenant, ok
}
func (s *MemoryStore) TenantBySlug(slug string) (domain.Tenant, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, tenant := range s.tenants {
		if tenant.Slug == slug {
			return tenant, true
		}
	}
	return domain.Tenant{}, false
}
func tenantKey(tenantID, id string) string { return tenantID + "\x00" + id }
func (s *MemoryStore) SaveWorker(tenantID string, worker domain.Worker) error {
	if tenantID == "" || worker.TenantID != tenantID || worker.Name == "" {
		return errors.New("worker tenant identity mismatch")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workers[tenantKey(tenantID, worker.Name)] = worker
	return nil
}
func (s *MemoryStore) Worker(tenantID, name string) (domain.Worker, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	worker, ok := s.workers[tenantKey(tenantID, name)]
	return worker, ok
}
func (s *MemoryStore) Workers(tenantID string) []domain.Worker {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.Worker, 0)
	for _, worker := range s.workers {
		if worker.TenantID == tenantID {
			out = append(out, worker)
		}
	}
	return out
}
func (s *MemoryStore) SaveWorkerVersion(tenantID string, d domain.WorkerVersion) error {
	if tenantID == "" || d.TenantID != tenantID {
		return errors.New("WorkerVersion tenant identity mismatch")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.versions[tenantKey(tenantID, d.ID)] = d
	return nil
}
func (s *MemoryStore) WorkerVersions(tenantID, workerName string) []domain.WorkerVersion {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []domain.WorkerVersion{}
	for _, d := range s.versions {
		if d.TenantID == tenantID && (workerName == "" || d.WorkerName == workerName) {
			out = append(out, d)
		}
	}
	return out
}
func (s *MemoryStore) WorkerVersion(tenantID, workerName, version string) (domain.WorkerVersion, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, item := range s.versions {
		if item.TenantID == tenantID && item.WorkerName == workerName && item.Version == version {
			return item, true
		}
	}
	return domain.WorkerVersion{}, false
}
func (s *MemoryStore) UpdateWorkerVersionDescription(tenantID, workerName, version string, expectedRevision int64, description string) (domain.WorkerVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, item := range s.versions {
		if item.TenantID == tenantID && item.WorkerName == workerName && item.Version == version {
			if item.Revision != expectedRevision {
				return domain.WorkerVersion{}, ErrConflict
			}
			item.Description, item.Revision, item.UpdatedAt = description, item.Revision+1, time.Now().UTC()
			s.versions[key] = item
			return item, nil
		}
	}
	return domain.WorkerVersion{}, ErrNotFound
}
func (s *MemoryStore) SaveInvocation(tenantID string, i domain.Invocation) error {
	if tenantID == "" || i.TenantID != tenantID {
		return errors.New("invocation tenant identity mismatch")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invocations[tenantKey(tenantID, i.ID)] = i
	return nil
}
func (s *MemoryStore) Invocation(tenantID, id string) (domain.Invocation, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	i, ok := s.invocations[tenantKey(tenantID, id)]
	return i, ok
}
func (s *MemoryStore) Invocations(tenantID string) []domain.Invocation {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.Invocation, 0)
	for _, invocation := range s.invocations {
		if invocation.TenantID == tenantID {
			out = append(out, invocation)
		}
	}
	return out
}
func (s *MemoryStore) InvocationByIdempotency(tenantID, workerName, workflow, key string) (domain.Invocation, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, i := range s.invocations {
		if i.TenantID == tenantID && i.WorkerName == workerName && i.Workflow == workflow && i.IdempotencyKey == key {
			return i, true
		}
	}
	return domain.Invocation{}, false
}
func actionOperationKey(tenantID, runID, nodeID, action, operationID string) string {
	return tenantID + "\x00" + runID + "\x00" + nodeID + "\x00" + action + "\x00" + operationID
}
func (s *MemoryStore) SaveActionOperation(tenantID string, operation domain.ActionOperation) error {
	if tenantID == "" || operation.TenantID != tenantID {
		return errors.New("action operation tenant identity mismatch")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.actionOperations[actionOperationKey(tenantID, operation.RunID, operation.RuntimeNodeID, operation.Action, operation.OperationID)] = operation
	return nil
}
func (s *MemoryStore) ActionOperation(tenantID, runID, nodeID, action, operationID string) (domain.ActionOperation, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	operation, ok := s.actionOperations[actionOperationKey(tenantID, runID, nodeID, action, operationID)]
	return operation, ok
}
func (s *MemoryStore) ActionOperations(tenantID, runID string) []domain.ActionOperation {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.ActionOperation, 0)
	for _, operation := range s.actionOperations {
		if operation.TenantID == tenantID && (runID == "" || operation.RunID == runID) {
			out = append(out, operation)
		}
	}
	return out
}
func (s *MemoryStore) AppendAudit(tenantID string, record domain.AuditRecord) error {
	if tenantID == "" || record.TenantID != tenantID {
		return errors.New("audit tenant identity mismatch")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.audits[tenantID] = append(s.audits[tenantID], record)
	return nil
}
func (s *MemoryStore) Audits(tenantID string) []domain.AuditRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]domain.AuditRecord(nil), s.audits[tenantID]...)
}
