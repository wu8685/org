package service

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/wu8685/org/internal/domain"
)

func TestFileStorePersistsAtomicTenantAndMembershipLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 2, 9, 0, 0, 0, time.UTC)
	tenant := domain.Tenant{ID: "tenant-managed", Slug: "managed", DisplayName: "Managed", Description: "Initial", Status: domain.TenantActive, QuotaPolicy: domain.DefaultTenantQuotaPolicy(), Revision: 1, CreatedAt: now, UpdatedAt: now}
	owner := domain.TenantMember{TenantID: tenant.ID, PrincipalID: "principal-owner", PrincipalDisplayName: "Owner", Role: domain.TenantRoleOwner, Revision: 1, CreatedAt: now, UpdatedAt: now}
	creationAudit := domain.AuditRecord{ID: "audit-create", TenantID: tenant.ID, PrincipalID: owner.PrincipalID, Action: "tenant.create", CreatedAt: now}
	if err := store.CommitTenantCreation(tenant, owner, creationAudit); err != nil {
		t.Fatal(err)
	}
	if err := store.CommitTenantCreation(tenant, owner, creationAudit); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate creation = %v", err)
	}

	tenant.DisplayName, tenant.Description, tenant.Revision, tenant.UpdatedAt = "Managed Team", "Updated", 2, now.Add(time.Minute)
	updateAudit := domain.AuditRecord{ID: "audit-update", TenantID: tenant.ID, PrincipalID: owner.PrincipalID, Action: "tenant.update", CreatedAt: tenant.UpdatedAt}
	if err := store.CommitTenantUpdate(tenant, 1, updateAudit); err != nil {
		t.Fatal(err)
	}
	if err := store.CommitTenantUpdate(tenant, 1, updateAudit); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale Tenant update = %v", err)
	}

	member := domain.TenantMember{TenantID: tenant.ID, PrincipalID: "principal-admin", PrincipalDisplayName: "Admin", Role: domain.TenantRoleAdmin, Revision: 1, CreatedAt: now, UpdatedAt: now}
	addAudit := domain.AuditRecord{ID: "audit-member-add", TenantID: tenant.ID, PrincipalID: owner.PrincipalID, Action: "tenant.member.add", CreatedAt: now}
	if err := store.CommitTenantMember(tenant.ID, member, 0, addAudit); err != nil {
		t.Fatal(err)
	}
	member.Role, member.Revision, member.UpdatedAt = domain.TenantRoleViewer, 2, now.Add(2*time.Minute)
	roleAudit := domain.AuditRecord{ID: "audit-member-role", TenantID: tenant.ID, PrincipalID: owner.PrincipalID, Action: "tenant.member.role.update", CreatedAt: member.UpdatedAt}
	if err := store.CommitTenantMember(tenant.ID, member, 1, roleAudit); err != nil {
		t.Fatal(err)
	}
	removeAudit := domain.AuditRecord{ID: "audit-member-remove", TenantID: tenant.ID, PrincipalID: owner.PrincipalID, Action: "tenant.member.remove", CreatedAt: now.Add(3 * time.Minute)}
	if err := store.CommitTenantMemberRemoval(tenant.ID, member.PrincipalID, 2, removeAudit); err != nil {
		t.Fatal(err)
	}
	if err := store.CommitTenantMemberRemoval(tenant.ID, owner.PrincipalID, 1, removeAudit); !errors.Is(err, ErrLastTenantOwner) {
		t.Fatalf("last owner removal = %v", err)
	}

	reopened, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reopened.Tenant(tenant.ID)
	if !ok || got.DisplayName != "Managed Team" || got.Description != "Updated" || got.Revision != 2 || got.Slug != "managed" {
		t.Fatalf("Tenant after restart = %#v, %v", got, ok)
	}
	if members := reopened.TenantMembers(tenant.ID); len(members) != 1 || members[0].PrincipalID != owner.PrincipalID || members[0].Role != domain.TenantRoleOwner {
		t.Fatalf("members after restart = %#v", members)
	}
	if memberships := reopened.TenantMembershipsForPrincipal(owner.PrincipalID); len(memberships) != 1 || memberships[0].TenantID != tenant.ID {
		t.Fatalf("principal memberships = %#v", memberships)
	}
	if audits := reopened.Audits(tenant.ID); len(audits) != 5 {
		t.Fatalf("atomic lifecycle Audits = %#v", audits)
	}
}

func TestTenantMemberRoleChangeCannotDemoteLastOwner(t *testing.T) {
	store := NewMemoryStore()
	now := time.Now().UTC()
	tenant := domain.Tenant{ID: "tenant-one-owner", Slug: "one-owner", DisplayName: "One Owner", Status: domain.TenantActive, QuotaPolicy: domain.DefaultTenantQuotaPolicy(), Revision: 1, CreatedAt: now, UpdatedAt: now}
	owner := domain.TenantMember{TenantID: tenant.ID, PrincipalID: "only-owner", PrincipalDisplayName: "Only Owner", Role: domain.TenantRoleOwner, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CommitTenantCreation(tenant, owner, domain.AuditRecord{ID: "audit-create", TenantID: tenant.ID}); err != nil {
		t.Fatal(err)
	}
	demoted := owner
	demoted.Role, demoted.Revision = domain.TenantRoleAdmin, 2
	if err := store.CommitTenantMember(tenant.ID, demoted, 1, domain.AuditRecord{ID: "audit-demote", TenantID: tenant.ID}); !errors.Is(err, ErrLastTenantOwner) {
		t.Fatalf("last owner demotion = %v", err)
	}
	if got, _ := store.TenantMember(tenant.ID, owner.PrincipalID); got.Role != domain.TenantRoleOwner || len(store.Audits(tenant.ID)) != 1 {
		t.Fatalf("failed demotion changed state: member=%#v audits=%#v", got, store.Audits(tenant.ID))
	}
}
