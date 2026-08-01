package service

import (
	"context"
	"sort"

	"github.com/wu8685/org/internal/domain"
)

type InvocationFilter struct {
	WorkerName    string
	Workflow      string
	WorkerVersion string
}

type WorkflowCatalogItem struct {
	WorkerName         string                  `json:"workerName"`
	WorkerVersion      string                  `json:"workerVersion"`
	VersionDescription string                  `json:"versionDescription"`
	Current            bool                    `json:"current"`
	Workflow           domain.WorkflowContract `json:"workflow"`
}

type CountSummary struct {
	Total int `json:"total"`
}

type VersionCountSummary struct {
	Total     int `json:"total"`
	Ready     int `json:"ready"`
	Deploying int `json:"deploying"`
	Failed    int `json:"failed"`
}

type QuotaUsage struct {
	ReservedCPUMilli      int64 `json:"reservedCpuMilli"`
	ReservedMemoryBytes   int64 `json:"reservedMemoryBytes"`
	ActiveWorkerPods      int   `json:"activeWorkerPods"`
	ActiveReleases        int   `json:"activeReleases"`
	ConcurrentRuns        int   `json:"concurrentRuns"`
	ConcurrentDeployments int   `json:"concurrentDeployments"`
}

type Overview struct {
	TenantID     string                   `json:"tenantId"`
	TenantStatus domain.TenantStatus      `json:"tenantStatus"`
	Workers      CountSummary             `json:"workers"`
	Versions     VersionCountSummary      `json:"versions"`
	Runs         CountSummary             `json:"runs"`
	QuotaPolicy  domain.TenantQuotaPolicy `json:"quotaPolicy"`
	QuotaUsage   QuotaUsage               `json:"quotaUsage"`
}

func (c *ControlPlane) GetOverview(_ context.Context, auth AuthenticatedContext) (result Overview, err error) {
	tenant, err := c.authorize(auth, PermissionWorkerRead, "overview.read", "tenant", auth.TenantID)
	if err != nil {
		return Overview{}, err
	}
	defer func() {
		c.auditAllowed(auth, tenant, PermissionWorkerRead, "overview.read", "tenant", tenant.ID, err, nil)
	}()
	result = Overview{
		TenantID: tenant.ID, TenantStatus: tenant.Status, QuotaPolicy: tenant.QuotaPolicy,
		Workers: CountSummary{Total: len(c.store.Workers(tenant.ID))},
		Runs:    CountSummary{Total: len(c.store.Invocations(tenant.ID))},
	}
	versions := c.store.WorkerVersions(tenant.ID, "")
	result.Versions.Total = len(versions)
	for _, version := range versions {
		switch version.State {
		case domain.WorkerVersionReady:
			result.Versions.Ready++
		case domain.WorkerVersionPending:
			result.Versions.Deploying++
		case domain.WorkerVersionFailed:
			result.Versions.Failed++
		}
	}
	for _, lease := range c.store.QuotaLeases(tenant.ID) {
		result.QuotaUsage.ReservedCPUMilli += lease.ReservedCPUMilli
		result.QuotaUsage.ReservedMemoryBytes += lease.ReservedMemoryBytes
		result.QuotaUsage.ActiveWorkerPods += lease.ActiveWorkerPods
		result.QuotaUsage.ActiveReleases += lease.ActiveReleases
		result.QuotaUsage.ConcurrentRuns += lease.ConcurrentRuns
		result.QuotaUsage.ConcurrentDeployments += lease.ConcurrentDeployments
	}
	return result, nil
}

func (c *ControlPlane) ListWorkers(_ context.Context, auth AuthenticatedContext) (result []domain.Worker, err error) {
	tenant, err := c.authorize(auth, PermissionWorkerRead, "worker.list", "worker", "")
	if err != nil {
		return nil, err
	}
	defer func() { c.auditAllowed(auth, tenant, PermissionWorkerRead, "worker.list", "worker", "", err, nil) }()
	result = c.store.Workers(tenant.ID)
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func (c *ControlPlane) ListWorkerVersions(_ context.Context, auth AuthenticatedContext, workerName string) (result []domain.WorkerVersion, err error) {
	tenant, err := c.authorize(auth, PermissionWorkerRead, "worker.version.list", "worker", workerName)
	if err != nil {
		return nil, err
	}
	defer func() {
		c.auditAllowed(auth, tenant, PermissionWorkerRead, "worker.version.list", "worker", workerName, err, nil)
	}()
	if _, ok := c.store.Worker(tenant.ID, workerName); !ok {
		return nil, ErrNotFound
	}
	result = c.store.WorkerVersions(tenant.ID, workerName)
	sort.Slice(result, func(i, j int) bool {
		if result[i].Current != result[j].Current {
			return result[i].Current
		}
		if !result[i].CreatedAt.Equal(result[j].CreatedAt) {
			return result[i].CreatedAt.After(result[j].CreatedAt)
		}
		return result[i].Version > result[j].Version
	})
	return result, nil
}

func (c *ControlPlane) GetWorkerVersion(_ context.Context, auth AuthenticatedContext, workerName, version string) (result domain.WorkerVersion, err error) {
	tenant, err := c.authorize(auth, PermissionWorkerRead, "worker.version.read", "workerVersion", workerName+"@"+version)
	if err != nil {
		return domain.WorkerVersion{}, err
	}
	defer func() {
		c.auditAllowed(auth, tenant, PermissionWorkerRead, "worker.version.read", "workerVersion", workerName+"@"+version, err, nil)
	}()
	result, ok := c.store.WorkerVersion(tenant.ID, workerName, version)
	if !ok {
		return domain.WorkerVersion{}, ErrNotFound
	}
	return result, nil
}

func (c *ControlPlane) ListInvocations(_ context.Context, auth AuthenticatedContext, filter InvocationFilter) (result []domain.Invocation, err error) {
	tenant, err := c.authorize(auth, PermissionRunRead, "run.list", "invocation", "")
	if err != nil {
		return nil, err
	}
	defer func() { c.auditAllowed(auth, tenant, PermissionRunRead, "run.list", "invocation", "", err, nil) }()
	for _, invocation := range c.store.Invocations(tenant.ID) {
		if filter.WorkerName != "" && invocation.WorkerName != filter.WorkerName {
			continue
		}
		if filter.Workflow != "" && invocation.Workflow != filter.Workflow {
			continue
		}
		if filter.WorkerVersion != "" && invocation.SelectedVersion != filter.WorkerVersion {
			continue
		}
		result = append(result, invocation)
	}
	sort.Slice(result, func(i, j int) bool {
		if !result[i].CreatedAt.Equal(result[j].CreatedAt) {
			return result[i].CreatedAt.After(result[j].CreatedAt)
		}
		return result[i].ID > result[j].ID
	})
	return result, nil
}

func (c *ControlPlane) ListRunActions(_ context.Context, auth AuthenticatedContext, runID string) (result []domain.ActionOperation, err error) {
	tenant, err := c.authorize(auth, PermissionRunRead, "run.action.list", "invocation", runID)
	if err != nil {
		return nil, err
	}
	defer func() {
		c.auditAllowed(auth, tenant, PermissionRunRead, "run.action.list", "invocation", runID, err, nil)
	}()
	if _, ok := c.store.Invocation(tenant.ID, runID); !ok {
		return nil, ErrNotFound
	}
	result = c.store.ActionOperations(tenant.ID, runID)
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, nil
}

func (c *ControlPlane) ListWorkflowCatalog(_ context.Context, auth AuthenticatedContext) (result []WorkflowCatalogItem, err error) {
	tenant, err := c.authorize(auth, PermissionWorkerRead, "workflow.list", "workflow", "")
	if err != nil {
		return nil, err
	}
	defer func() { c.auditAllowed(auth, tenant, PermissionWorkerRead, "workflow.list", "workflow", "", err, nil) }()
	for _, version := range c.store.WorkerVersions(tenant.ID, "") {
		if version.State != domain.WorkerVersionReady {
			continue
		}
		for _, workflow := range version.Metadata.Workflows {
			result = append(result, WorkflowCatalogItem{
				WorkerName: version.WorkerName, WorkerVersion: version.Version, VersionDescription: version.Description,
				Current: version.Current, Workflow: workflow,
			})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].WorkerName != result[j].WorkerName {
			return result[i].WorkerName < result[j].WorkerName
		}
		if result[i].Current != result[j].Current {
			return result[i].Current
		}
		if result[i].WorkerVersion != result[j].WorkerVersion {
			return result[i].WorkerVersion > result[j].WorkerVersion
		}
		return result[i].Workflow.Name < result[j].Workflow.Name
	})
	return result, nil
}
