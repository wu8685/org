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
