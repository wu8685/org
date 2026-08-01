package config

import "testing"

func TestDefaultsSeparateHostAndKindPodTemporalEndpoints(t *testing.T) {
	cfg, err := Load(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TemporalAddress != "127.0.0.1:7233" {
		t.Fatalf("host address = %q", cfg.TemporalAddress)
	}
	if cfg.WorkerTemporalAddress != "host.docker.internal:7233" {
		t.Fatalf("Worker address = %q", cfg.WorkerTemporalAddress)
	}
	if cfg.KubeContext != "kind-org" {
		t.Fatalf("context = %q", cfg.KubeContext)
	}
	if cfg.ConsoleAddress != "127.0.0.1:8090" || cfg.ConsoleTenantSlug != "local" {
		t.Fatalf("console defaults = %#v", cfg)
	}
}

func TestProductionContextAndEndpointsAreConfigurable(t *testing.T) {
	env := map[string]string{"ORG_TEMPORAL_ADDRESS": "cloud.example.com:7233", "ORG_WORKER_TEMPORAL_ADDRESS": "temporal.internal:7233", "ORG_KUBE_CONTEXT": "production", "ORG_KUBECONFIG": "/secure/kubeconfig", "ORG_REGISTRY_ALLOWLIST": "registry.example.com,ghcr.io"}
	cfg, err := Load(func(key string) string { return env[key] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.KubeContext != "production" || cfg.Kubeconfig != "/secure/kubeconfig" || len(cfg.RegistryAllowlist) != 2 {
		t.Fatalf("config = %#v", cfg)
	}
}

func TestKindRejectsWorkerLocalhostBecauseItPointsAtThePod(t *testing.T) {
	env := map[string]string{"ORG_WORKER_TEMPORAL_ADDRESS": "127.0.0.1:7233"}
	if _, err := Load(func(key string) string { return env[key] }); err == nil {
		t.Fatal("expected kind Worker localhost rejection")
	}
}

func TestConsoleDevelopmentIdentityIsConfigurableWithoutRequestTenantHeaders(t *testing.T) {
	env := map[string]string{
		"ORG_CONSOLE_ADDRESS": "127.0.0.1:9090", "ORG_CONSOLE_TENANT_ID": "tenant-demo",
		"ORG_CONSOLE_TENANT_SLUG": "demo", "ORG_CONSOLE_TENANT_NAME": "Demo Studio", "ORG_CONSOLE_PRINCIPAL_ID": "developer",
	}
	cfg, err := Load(func(key string) string { return env[key] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ConsoleAddress != "127.0.0.1:9090" || cfg.ConsoleTenantID != "tenant-demo" || cfg.ConsoleTenantSlug != "demo" || cfg.ConsoleTenantName != "Demo Studio" || cfg.ConsolePrincipalID != "developer" {
		t.Fatalf("config=%#v", cfg)
	}
}
