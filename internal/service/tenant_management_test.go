package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wu8685/org/internal/domain"
)

func TestTenantManagementLifecycleIsMembershipScopedAndAudited(t *testing.T) {
	store := NewMemoryStore()
	now := time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)
	local := domain.Tenant{ID: "tenant-local", Slug: "local", DisplayName: "Local Development", Status: domain.TenantActive, QuotaPolicy: domain.DefaultTenantQuotaPolicy(), Revision: 1, CreatedAt: now, UpdatedAt: now}
	alice := Principal{ID: "alice", DisplayName: "Alice"}
	bob := Principal{ID: "bob", DisplayName: "Bob"}
	owner := domain.TenantMember{TenantID: local.ID, PrincipalID: alice.ID, PrincipalDisplayName: alice.DisplayName, Role: domain.TenantRoleOwner, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CommitTenantCreation(local, owner, domain.AuditRecord{ID: "bootstrap", TenantID: local.ID}); err != nil {
		t.Fatal(err)
	}
	cp := New(Config{PrincipalDirectory: NewStaticPrincipalDirectory(alice, bob)}, store, &fakeCluster{}, &fakeExecutor{})
	auth := AuthenticatedContext{PrincipalID: alice.ID, TenantID: local.ID, TenantSlug: local.Slug, AuthenticationMethod: "test", RequestID: "request-create", Permissions: domain.TenantRolePermissions(domain.TenantRoleOwner)}

	created, err := cp.CreateTenant(context.Background(), auth, CreateTenantRequest{Slug: "studio", DisplayName: "Studio", Description: "Product workflows"})
	if err != nil {
		t.Fatal(err)
	}
	if created.Tenant.Slug != "studio" || created.Tenant.Revision != 1 || created.Membership.Role != domain.TenantRoleOwner || created.Membership.PrincipalID != alice.ID {
		t.Fatalf("created=%#v", created)
	}
	listed, err := cp.ListTenants(context.Background(), auth)
	if err != nil || len(listed) != 2 || listed[0].Tenant.Slug != "local" || listed[1].Tenant.Slug != "studio" {
		t.Fatalf("listed=%#v err=%v", listed, err)
	}

	detail, err := cp.GetTenant(context.Background(), auth, "studio")
	if err != nil || len(detail.Members) != 1 || !detail.AllowedActions["update"] || !detail.AllowedActions["manageMembers"] {
		t.Fatalf("detail=%#v err=%v", detail, err)
	}
	updated, err := cp.UpdateTenant(context.Background(), auth, "studio", 1, UpdateTenantRequest{DisplayName: "Studio Team", Description: "Updated", QuotaPolicy: domain.DefaultTenantQuotaPolicy()})
	if err != nil || updated.Tenant.Revision != 2 || updated.Tenant.DisplayName != "Studio Team" || updated.Tenant.Slug != "studio" {
		t.Fatalf("updated=%#v err=%v", updated, err)
	}
	if _, err := cp.UpdateTenant(context.Background(), auth, "studio", 1, UpdateTenantRequest{DisplayName: "Stale", QuotaPolicy: domain.DefaultTenantQuotaPolicy()}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update=%v", err)
	}

	member, err := cp.AddTenantMember(context.Background(), auth, "studio", AddTenantMemberRequest{PrincipalID: bob.ID, Role: domain.TenantRoleAdmin})
	if err != nil || member.Role != domain.TenantRoleAdmin || member.PrincipalDisplayName != bob.DisplayName {
		t.Fatalf("member=%#v err=%v", member, err)
	}
	if _, err := cp.AddTenantMember(context.Background(), auth, "studio", AddTenantMemberRequest{PrincipalID: "unknown", Role: domain.TenantRoleViewer}); !errors.Is(err, ErrPrincipalNotFound) {
		t.Fatalf("unknown principal=%v", err)
	}
	member, err = cp.UpdateTenantMemberRole(context.Background(), auth, "studio", bob.ID, 1, domain.TenantRoleViewer)
	if err != nil || member.Role != domain.TenantRoleViewer || member.Revision != 2 {
		t.Fatalf("role update=%#v err=%v", member, err)
	}
	if err := cp.RemoveTenantMember(context.Background(), auth, "studio", bob.ID, 2); err != nil {
		t.Fatal(err)
	}
	if err := cp.RemoveTenantMember(context.Background(), auth, "studio", alice.ID, 1); !errors.Is(err, ErrLastTenantOwner) {
		t.Fatalf("last owner remove=%v", err)
	}

	audits := store.Audits(created.Tenant.ID)
	wantActions := map[string]bool{"tenant.create": false, "tenant.update": false, "tenant.member.add": false, "tenant.member.role.update": false, "tenant.member.remove": false}
	for _, audit := range audits {
		if _, ok := wantActions[audit.Action]; ok {
			wantActions[audit.Action] = true
			if audit.TenantID != created.Tenant.ID || audit.PrincipalID != alice.ID {
				t.Fatalf("mis-scoped Audit=%#v", audit)
			}
		}
	}
	for action, found := range wantActions {
		if !found {
			t.Errorf("missing Audit %s in %#v", action, audits)
		}
	}
}

func TestTenantManagementRejectsIDORAndQuotaBelowDurableUsage(t *testing.T) {
	store := NewMemoryStore()
	now := time.Now().UTC()
	alice := Principal{ID: "alice", DisplayName: "Alice"}
	bob := Principal{ID: "bob", DisplayName: "Bob"}
	for _, value := range []struct {
		tenant domain.Tenant
		owner  domain.TenantMember
	}{
		{tenant: domain.Tenant{ID: "tenant-a", Slug: "alpha", DisplayName: "Alpha", Status: domain.TenantActive, QuotaPolicy: domain.DefaultTenantQuotaPolicy(), Revision: 1, CreatedAt: now, UpdatedAt: now}, owner: domain.TenantMember{TenantID: "tenant-a", PrincipalID: alice.ID, PrincipalDisplayName: alice.DisplayName, Role: domain.TenantRoleOwner, Revision: 1, CreatedAt: now, UpdatedAt: now}},
		{tenant: domain.Tenant{ID: "tenant-b", Slug: "beta", DisplayName: "Beta", Status: domain.TenantActive, QuotaPolicy: domain.DefaultTenantQuotaPolicy(), Revision: 1, CreatedAt: now, UpdatedAt: now}, owner: domain.TenantMember{TenantID: "tenant-b", PrincipalID: bob.ID, PrincipalDisplayName: bob.DisplayName, Role: domain.TenantRoleOwner, Revision: 1, CreatedAt: now, UpdatedAt: now}},
	} {
		if err := store.CommitTenantCreation(value.tenant, value.owner, domain.AuditRecord{ID: "bootstrap-" + value.tenant.ID, TenantID: value.tenant.ID}); err != nil {
			t.Fatal(err)
		}
	}
	cp := New(Config{PrincipalDirectory: NewStaticPrincipalDirectory(alice, bob)}, store, &fakeCluster{}, &fakeExecutor{})
	authAlice := AuthenticatedContext{PrincipalID: alice.ID, TenantID: "tenant-a", TenantSlug: "alpha", AuthenticationMethod: "test", Permissions: domain.TenantRolePermissions(domain.TenantRoleOwner)}
	if _, err := cp.GetTenant(context.Background(), authAlice, "beta"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-Tenant get=%v", err)
	}
	if _, err := cp.UpdateTenant(context.Background(), authAlice, "beta", 1, UpdateTenantRequest{DisplayName: "Forged", QuotaPolicy: domain.DefaultTenantQuotaPolicy()}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-Tenant update=%v", err)
	}
	if tenants, err := cp.ListTenants(context.Background(), authAlice); err != nil || len(tenants) != 1 || tenants[0].Tenant.Slug != "alpha" {
		t.Fatalf("unauthorized Tenant enumerated: %#v err=%v", tenants, err)
	}

	if err := store.AcquireQuotaLease("tenant-a", domain.QuotaLease{ID: "run:active", TenantID: "tenant-a", Kind: domain.QuotaLeaseRun, ConcurrentRuns: 2, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	tooLow := domain.DefaultTenantQuotaPolicy()
	tooLow.MaxConcurrentRuns = 1
	if _, err := cp.UpdateTenant(context.Background(), authAlice, "alpha", 1, UpdateTenantRequest{DisplayName: "Alpha", QuotaPolicy: tooLow}); !errors.Is(err, ErrQuotaBelowCurrentUsage) {
		t.Fatalf("quota below usage=%v", err)
	}

	viewer := domain.TenantMember{TenantID: "tenant-a", PrincipalID: bob.ID, PrincipalDisplayName: bob.DisplayName, Role: domain.TenantRoleViewer, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CommitTenantMember("tenant-a", viewer, 0, domain.AuditRecord{ID: "add-viewer", TenantID: "tenant-a"}); err != nil {
		t.Fatal(err)
	}
	authBob := AuthenticatedContext{PrincipalID: bob.ID, TenantID: "tenant-a", TenantSlug: "alpha", AuthenticationMethod: "test", Permissions: domain.TenantRolePermissions(domain.TenantRoleViewer)}
	detail, err := cp.GetTenant(context.Background(), authBob, "alpha")
	if err != nil || len(detail.Members) != 1 || detail.Members[0].PrincipalID != bob.ID || detail.AllowedActions["update"] {
		t.Fatalf("viewer detail=%#v err=%v", detail, err)
	}
	if _, err := cp.UpdateTenant(context.Background(), authBob, "alpha", 1, UpdateTenantRequest{DisplayName: "Denied", QuotaPolicy: domain.DefaultTenantQuotaPolicy()}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("viewer update must hide target=%v", err)
	}
	denied := false
	for _, audit := range store.Audits("tenant-a") {
		if audit.Action == "tenant.update" && audit.PrincipalID == bob.ID && audit.AuthorizationResult == "denied" && audit.Outcome == "denied" && audit.TargetID == "" {
			denied = true
		}
	}
	if !denied {
		t.Fatalf("Tenant management denial was not safely audited: %#v", store.Audits("tenant-a"))
	}
}
