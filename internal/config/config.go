package config

import (
	"errors"
	"net"
	"strings"
)

type Config struct {
	StateFile             string
	TemporalAddress       string
	WorkerTemporalAddress string
	TemporalNamespace     string
	TemporalWebBaseURL    string
	KubeContext           string
	Kubeconfig            string
	KubeNamespace         string
	RegistryAllowlist     []string
	ConsoleAddress        string
	ConsoleTenantID       string
	ConsoleTenantSlug     string
	ConsoleTenantName     string
	ConsolePrincipalID    string
}

func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		StateFile: ".org/state.json", TemporalAddress: "127.0.0.1:7233", WorkerTemporalAddress: "host.docker.internal:7233",
		TemporalNamespace: "default", TemporalWebBaseURL: "http://127.0.0.1:8080", KubeContext: "kind-org",
		KubeNamespace: "org-workers", RegistryAllowlist: []string{"ghcr.io"},
		ConsoleAddress: "127.0.0.1:8090", ConsoleTenantID: "tenant-local", ConsoleTenantSlug: "local",
		ConsoleTenantName: "Local Development", ConsolePrincipalID: "local-developer",
	}
	set(&cfg.StateFile, getenv("ORG_STATE_FILE"))
	set(&cfg.TemporalAddress, getenv("ORG_TEMPORAL_ADDRESS"))
	set(&cfg.WorkerTemporalAddress, getenv("ORG_WORKER_TEMPORAL_ADDRESS"))
	set(&cfg.TemporalNamespace, getenv("ORG_TEMPORAL_NAMESPACE"))
	set(&cfg.TemporalWebBaseURL, getenv("ORG_TEMPORAL_WEB_URL"))
	set(&cfg.KubeContext, getenv("ORG_KUBE_CONTEXT"))
	set(&cfg.Kubeconfig, getenv("ORG_KUBECONFIG"))
	set(&cfg.KubeNamespace, getenv("ORG_KUBE_NAMESPACE"))
	set(&cfg.ConsoleAddress, getenv("ORG_CONSOLE_ADDRESS"))
	set(&cfg.ConsoleTenantID, getenv("ORG_CONSOLE_TENANT_ID"))
	set(&cfg.ConsoleTenantSlug, getenv("ORG_CONSOLE_TENANT_SLUG"))
	set(&cfg.ConsoleTenantName, getenv("ORG_CONSOLE_TENANT_NAME"))
	set(&cfg.ConsolePrincipalID, getenv("ORG_CONSOLE_PRINCIPAL_ID"))
	if value := strings.TrimSpace(getenv("ORG_REGISTRY_ALLOWLIST")); value != "" {
		cfg.RegistryAllowlist = splitList(value)
	}
	if strings.HasPrefix(cfg.KubeContext, "kind-") && isLoopbackAddress(cfg.WorkerTemporalAddress) {
		return Config{}, errors.New("Worker Temporal address cannot be localhost for a kind cluster")
	}
	if len(cfg.RegistryAllowlist) == 0 {
		return Config{}, errors.New("at least one image registry must be allowlisted")
	}
	return cfg, nil
}

func set(target *string, value string) {
	if strings.TrimSpace(value) != "" {
		*target = strings.TrimSpace(value)
	}
}
func splitList(value string) []string {
	out := []string{}
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}
func isLoopbackAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	host = strings.Trim(host, "[]")
	return host == "localhost" || net.ParseIP(host).IsLoopback()
}
