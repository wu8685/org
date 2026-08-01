package service

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/wu8685/org/internal/domain"
)

type Principal struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

type PrincipalDirectory interface {
	Principal(string) (Principal, bool)
}

type staticPrincipalDirectory struct {
	principals map[string]Principal
}

func NewStaticPrincipalDirectory(principals ...Principal) PrincipalDirectory {
	directory := &staticPrincipalDirectory{principals: map[string]Principal{}}
	for _, principal := range principals {
		if principal.ID != "" && principal.DisplayName != "" {
			directory.principals[principal.ID] = principal
		}
	}
	return directory
}

func (d *staticPrincipalDirectory) Principal(id string) (Principal, bool) {
	principal, ok := d.principals[id]
	return principal, ok
}

type CreateTenantRequest struct {
	Slug        string `json:"slug"`
	DisplayName string `json:"displayName"`
	Description string `json:"description,omitempty"`
}

type UpdateTenantRequest struct {
	DisplayName string                   `json:"displayName"`
	Description string                   `json:"description,omitempty"`
	QuotaPolicy domain.TenantQuotaPolicy `json:"quotaPolicy"`
}

type AddTenantMemberRequest struct {
	PrincipalID string            `json:"principalId"`
	Role        domain.TenantRole `json:"role"`
}

type TenantAccessView struct {
	Tenant         domain.Tenant       `json:"tenant"`
	Membership     domain.TenantMember `json:"membership"`
	Permissions    []string            `json:"permissions"`
	QuotaUsage     QuotaUsage          `json:"quotaUsage"`
	Members        []TenantMemberView  `json:"members,omitempty"`
	AllowedActions map[string]bool     `json:"allowedActions"`
}

type TenantMemberView struct {
	domain.TenantMember
	Permissions []string `json:"permissions"`
}

func tenantMemberView(member domain.TenantMember) TenantMemberView {
	return TenantMemberView{TenantMember: member, Permissions: permissionsList(domain.TenantRolePermissions(member.Role))}
}

func permissionsList(permissions map[string]bool) []string {
	result := make([]string, 0, len(permissions))
	for permission, allowed := range permissions {
		if allowed {
			result = append(result, permission)
		}
	}
	sort.Strings(result)
	return result
}

func (c *ControlPlane) tenantUsage(tenantID string) QuotaUsage {
	var usage QuotaUsage
	for _, lease := range c.store.QuotaLeases(tenantID) {
		usage.ReservedCPUMilli += lease.ReservedCPUMilli
		usage.ReservedMemoryBytes += lease.ReservedMemoryBytes
		usage.ActiveWorkerPods += lease.ActiveWorkerPods
		usage.ActiveReleases += lease.ActiveReleases
		usage.ConcurrentRuns += lease.ConcurrentRuns
		usage.ConcurrentDeployments += lease.ConcurrentDeployments
	}
	return usage
}

func tenantAllowedActions(permissions map[string]bool) map[string]bool {
	return map[string]bool{
		"update": permissions[PermissionTenantUpdate], "manageMembers": permissions[PermissionTenantMemberManage], "create": permissions[PermissionTenantCreate],
	}
}

func (c *ControlPlane) tenantView(tenant domain.Tenant, membership domain.TenantMember, includeMembers bool) TenantAccessView {
	permissions := domain.TenantRolePermissions(membership.Role)
	view := TenantAccessView{Tenant: tenant, Membership: membership, Permissions: permissionsList(permissions), QuotaUsage: c.tenantUsage(tenant.ID), AllowedActions: tenantAllowedActions(permissions)}
	if includeMembers {
		members := c.store.TenantMembers(tenant.ID)
		view.Members = make([]TenantMemberView, 0, len(members))
		for _, member := range members {
			view.Members = append(view.Members, tenantMemberView(member))
		}
	} else {
		view.Members = []TenantMemberView{tenantMemberView(membership)}
	}
	return view
}

func (c *ControlPlane) ListTenants(_ context.Context, auth AuthenticatedContext) ([]TenantAccessView, error) {
	if auth.PrincipalID == "" || auth.AuthenticationMethod == "" {
		return nil, ErrUnauthenticated
	}
	views := make([]TenantAccessView, 0)
	for _, membership := range c.store.TenantMembershipsForPrincipal(auth.PrincipalID) {
		permissions := domain.TenantRolePermissions(membership.Role)
		if !permissions[PermissionTenantRead] {
			continue
		}
		tenant, ok := c.store.Tenant(membership.TenantID)
		if !ok {
			continue
		}
		views = append(views, c.tenantView(tenant, membership, false))
	}
	sort.Slice(views, func(i, j int) bool { return views[i].Tenant.Slug < views[j].Tenant.Slug })
	return views, nil
}

func (c *ControlPlane) managementAccess(auth AuthenticatedContext, tenantSlug, permission, action string) (domain.Tenant, domain.TenantMember, map[string]bool, error) {
	if auth.PrincipalID == "" || auth.AuthenticationMethod == "" {
		return domain.Tenant{}, domain.TenantMember{}, nil, ErrUnauthenticated
	}
	tenant, ok := c.store.TenantBySlug(tenantSlug)
	if !ok {
		c.auditTenantManagementDenial(auth, permission, action)
		return domain.Tenant{}, domain.TenantMember{}, nil, ErrNotFound
	}
	membership, ok := c.store.TenantMember(tenant.ID, auth.PrincipalID)
	if !ok {
		c.auditTenantManagementDenial(auth, permission, action)
		return domain.Tenant{}, domain.TenantMember{}, nil, ErrNotFound
	}
	permissions := domain.TenantRolePermissions(membership.Role)
	if !permissions[permission] {
		c.auditTenantManagementDenial(auth, permission, action)
		return domain.Tenant{}, domain.TenantMember{}, nil, ErrNotFound
	}
	return tenant, membership, permissions, nil
}

func (c *ControlPlane) GetTenant(_ context.Context, auth AuthenticatedContext, tenantSlug string) (TenantAccessView, error) {
	tenant, membership, permissions, err := c.managementAccess(auth, tenantSlug, PermissionTenantRead, "tenant.read")
	if err != nil {
		return TenantAccessView{}, err
	}
	return c.tenantView(tenant, membership, permissions[PermissionTenantMemberManage]), nil
}

func (c *ControlPlane) CreateTenant(_ context.Context, auth AuthenticatedContext, request CreateTenantRequest) (TenantAccessView, error) {
	if _, err := c.authorize(auth, PermissionTenantCreate, "tenant.create", "tenant", ""); err != nil {
		return TenantAccessView{}, err
	}
	principal, ok := c.configuredPrincipal(auth.PrincipalID)
	if !ok {
		c.auditTenantManagementFailure(auth, domain.Tenant{}, PermissionTenantCreate, "tenant.create", ErrPrincipalNotFound)
		return TenantAccessView{}, ErrPrincipalNotFound
	}
	now := c.publishNow()
	tenant := domain.Tenant{ID: newID("tenant"), Slug: strings.TrimSpace(request.Slug), DisplayName: strings.TrimSpace(request.DisplayName), Description: strings.TrimSpace(request.Description), Status: domain.TenantActive, QuotaPolicy: domain.DefaultTenantQuotaPolicy(), Revision: 1, CreatedAt: now, UpdatedAt: now}
	owner := domain.TenantMember{TenantID: tenant.ID, PrincipalID: principal.ID, PrincipalDisplayName: principal.DisplayName, Role: domain.TenantRoleOwner, Revision: 1, CreatedAt: now, UpdatedAt: now}
	audit := c.tenantMutationAudit(auth, tenant, PermissionTenantCreate, "tenant.create", tenant.ID, nil)
	if err := c.store.CommitTenantCreation(tenant, owner, audit); err != nil {
		c.auditTenantManagementFailure(auth, domain.Tenant{}, PermissionTenantCreate, "tenant.create", err)
		if errors.Is(err, ErrConflict) {
			return TenantAccessView{}, ErrTenantSlugConflict
		}
		return TenantAccessView{}, err
	}
	return c.tenantView(tenant, owner, true), nil
}

func (c *ControlPlane) UpdateTenant(_ context.Context, auth AuthenticatedContext, tenantSlug string, expectedRevision int64, request UpdateTenantRequest) (TenantAccessView, error) {
	tenant, membership, permissions, err := c.managementAccess(auth, tenantSlug, PermissionTenantUpdate, "tenant.update")
	if err != nil {
		return TenantAccessView{}, err
	}
	updated := tenant
	updated.DisplayName, updated.Description, updated.QuotaPolicy = strings.TrimSpace(request.DisplayName), strings.TrimSpace(request.Description), request.QuotaPolicy
	updated.Revision, updated.UpdatedAt = expectedRevision+1, c.publishNow()
	if err := c.ensureQuotaNotBelowUsage(updated.QuotaPolicy, c.tenantUsage(tenant.ID)); err != nil {
		c.auditTenantManagementFailure(auth, tenant, PermissionTenantUpdate, "tenant.update", err)
		return TenantAccessView{}, err
	}
	audit := c.tenantMutationAudit(auth, tenant, PermissionTenantUpdate, "tenant.update", tenant.ID, nil)
	if err := c.store.CommitTenantUpdate(updated, expectedRevision, audit); err != nil {
		c.auditTenantManagementFailure(auth, tenant, PermissionTenantUpdate, "tenant.update", err)
		return TenantAccessView{}, err
	}
	return c.tenantView(updated, membership, permissions[PermissionTenantMemberManage]), nil
}

func (c *ControlPlane) AddTenantMember(_ context.Context, auth AuthenticatedContext, tenantSlug string, request AddTenantMemberRequest) (domain.TenantMember, error) {
	tenant, _, _, err := c.managementAccess(auth, tenantSlug, PermissionTenantMemberManage, "tenant.member.add")
	if err != nil {
		return domain.TenantMember{}, err
	}
	principal, ok := c.configuredPrincipal(strings.TrimSpace(request.PrincipalID))
	if !ok {
		c.auditTenantManagementFailure(auth, tenant, PermissionTenantMemberManage, "tenant.member.add", ErrPrincipalNotFound)
		return domain.TenantMember{}, ErrPrincipalNotFound
	}
	now := c.publishNow()
	member := domain.TenantMember{TenantID: tenant.ID, PrincipalID: principal.ID, PrincipalDisplayName: principal.DisplayName, Role: request.Role, Revision: 1, CreatedAt: now, UpdatedAt: now}
	audit := c.tenantMutationAudit(auth, tenant, PermissionTenantMemberManage, "tenant.member.add", principal.ID, map[string]string{"affectedPrincipalId": principal.ID, "role": string(request.Role)})
	if err := c.store.CommitTenantMember(tenant.ID, member, 0, audit); err != nil {
		c.auditTenantManagementFailure(auth, tenant, PermissionTenantMemberManage, "tenant.member.add", err)
		return domain.TenantMember{}, err
	}
	if existing, ok := c.store.TenantMember(tenant.ID, principal.ID); ok {
		return existing, nil
	}
	return member, nil
}

func (c *ControlPlane) UpdateTenantMemberRole(_ context.Context, auth AuthenticatedContext, tenantSlug, principalID string, expectedRevision int64, role domain.TenantRole) (domain.TenantMember, error) {
	tenant, _, _, err := c.managementAccess(auth, tenantSlug, PermissionTenantMemberManage, "tenant.member.role.update")
	if err != nil {
		return domain.TenantMember{}, err
	}
	member, ok := c.store.TenantMember(tenant.ID, principalID)
	if !ok {
		c.auditTenantManagementFailure(auth, tenant, PermissionTenantMemberManage, "tenant.member.role.update", ErrNotFound)
		return domain.TenantMember{}, ErrNotFound
	}
	member.Role, member.Revision, member.UpdatedAt = role, expectedRevision+1, c.publishNow()
	audit := c.tenantMutationAudit(auth, tenant, PermissionTenantMemberManage, "tenant.member.role.update", principalID, map[string]string{"affectedPrincipalId": principalID, "role": string(role)})
	if err := c.store.CommitTenantMember(tenant.ID, member, expectedRevision, audit); err != nil {
		c.auditTenantManagementFailure(auth, tenant, PermissionTenantMemberManage, "tenant.member.role.update", err)
		return domain.TenantMember{}, err
	}
	return member, nil
}

func (c *ControlPlane) RemoveTenantMember(_ context.Context, auth AuthenticatedContext, tenantSlug, principalID string, expectedRevision int64) error {
	tenant, _, _, err := c.managementAccess(auth, tenantSlug, PermissionTenantMemberManage, "tenant.member.remove")
	if err != nil {
		return err
	}
	audit := c.tenantMutationAudit(auth, tenant, PermissionTenantMemberManage, "tenant.member.remove", principalID, map[string]string{"affectedPrincipalId": principalID})
	if err := c.store.CommitTenantMemberRemoval(tenant.ID, principalID, expectedRevision, audit); err != nil {
		c.auditTenantManagementFailure(auth, tenant, PermissionTenantMemberManage, "tenant.member.remove", err)
		return err
	}
	return nil
}

func (c *ControlPlane) configuredPrincipal(id string) (Principal, bool) {
	if c.cfg.PrincipalDirectory == nil {
		return Principal{}, false
	}
	return c.cfg.PrincipalDirectory.Principal(id)
}

func (c *ControlPlane) tenantMutationAudit(auth AuthenticatedContext, tenant domain.Tenant, permission, action, targetID string, references map[string]string) domain.AuditRecord {
	return domain.AuditRecord{ID: newID("aud"), TenantID: tenant.ID, TenantSlug: tenant.Slug, PrincipalID: auth.PrincipalID, AuthenticationMethod: auth.AuthenticationMethod, RequestID: auth.RequestID, Action: action, Permission: permission, AuthorizationResult: "allowed", Outcome: "success", TargetType: "tenant", TargetID: targetID, References: references, CreatedAt: c.publishNow()}
}

func (c *ControlPlane) auditTenantManagementDenial(auth AuthenticatedContext, permission, action string) {
	tenant, _ := c.store.Tenant(auth.TenantID)
	c.audit(auth, tenant, permission, action, "tenant", "", "denied", "denied", "not_found", nil)
}

func (c *ControlPlane) auditTenantManagementFailure(auth AuthenticatedContext, tenant domain.Tenant, permission, action string, operationErr error) {
	if tenant.ID == "" {
		tenant, _ = c.store.Tenant(auth.TenantID)
	}
	c.auditAllowed(auth, tenant, permission, action, "tenant", tenant.ID, operationErr, nil)
}

func (c *ControlPlane) ensureQuotaNotBelowUsage(policy domain.TenantQuotaPolicy, usage QuotaUsage) error {
	cpu, cpuErr := parseCPU(policy.MaxReservedCPU)
	memory, memoryErr := parseMemory(policy.MaxReservedMemory)
	if cpuErr != nil || memoryErr != nil {
		return errors.New("tenant quota policy is invalid")
	}
	if usage.ReservedCPUMilli > cpu || usage.ReservedMemoryBytes > memory || usage.ActiveWorkerPods > policy.MaxActiveWorkerPods || usage.ActiveReleases > policy.MaxActiveReleases || usage.ConcurrentRuns > policy.MaxConcurrentRuns || usage.ConcurrentDeployments > policy.MaxConcurrentDeployments {
		return ErrQuotaBelowCurrentUsage
	}
	return nil
}
