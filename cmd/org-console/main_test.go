package main

import (
	"testing"

	"github.com/wu8685/org/internal/config"
	"github.com/wu8685/org/internal/service"
)

func TestDevelopmentTenantBootstrapAndIdentityAreServerConfigured(t *testing.T) {
	cfg := config.Config{ConsoleTenantID: "tenant-local", ConsoleTenantSlug: "local", ConsoleTenantName: "Local", ConsolePrincipalID: "developer"}
	store := service.NewMemoryStore()
	if err := ensureDevelopmentTenant(store, cfg); err != nil {
		t.Fatal(err)
	}
	if err := ensureDevelopmentTenant(store, cfg); err != nil {
		t.Fatalf("bootstrap must be idempotent: %v", err)
	}
	tenant, ok := store.Tenant("tenant-local")
	if !ok || tenant.Slug != "local" {
		t.Fatalf("tenant=%#v ok=%v", tenant, ok)
	}
	identity := developmentIdentity(cfg, "csrf-random")
	if identity.Auth.TenantID != "tenant-local" || identity.Auth.PrincipalID != "developer" || identity.CSRFToken != "csrf-random" || !identity.Auth.Permissions[service.PermissionWorkerDeploy] || !identity.Auth.Permissions[service.PermissionRunStart] || !identity.Auth.Permissions[service.PermissionRunCancel] {
		t.Fatalf("identity=%#v", identity)
	}
}

func TestDevelopmentTenantCatalogBootstrapsEveryAuthorizedTenant(t *testing.T) {
	cfg := config.Config{
		ConsoleTenantID: "tenant-a", ConsoleTenantSlug: "alpha", ConsoleTenantName: "Alpha", ConsolePrincipalID: "developer",
		ConsoleTenants: []config.ConsoleTenant{{ID: "tenant-a", Slug: "alpha", DisplayName: "Alpha"}, {ID: "tenant-b", Slug: "beta", DisplayName: "Beta"}},
	}
	store := service.NewMemoryStore()
	if err := ensureDevelopmentTenants(store, cfg); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Tenant("tenant-a"); !ok {
		t.Fatal("default Tenant was not created")
	}
	if tenant, ok := store.Tenant("tenant-b"); !ok || tenant.Slug != "beta" {
		t.Fatalf("second Tenant=%#v exists=%v", tenant, ok)
	}
	memberships := developmentMemberships(cfg)
	if len(memberships) != 2 || memberships[1].TenantID != "tenant-b" || !memberships[1].Permissions[service.PermissionRunStart] {
		t.Fatalf("memberships=%#v", memberships)
	}
}
