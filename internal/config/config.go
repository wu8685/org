package config

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
)

type ConsoleTenant struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	DisplayName string `json:"displayName"`
}

type ConsolePrincipal struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

type Config struct {
	StateFile               string
	TemporalAddress         string
	WorkerTemporalAddress   string
	WorkerBootstrapEndpoint string
	TemporalNamespace       string
	TemporalWebBaseURL      string
	KubeContext             string
	Kubeconfig              string
	KubeNamespace           string
	RegistryAllowlist       []string
	ConsoleAddress          string
	ConsoleTenantID         string
	ConsoleTenantSlug       string
	ConsoleTenantName       string
	ConsolePrincipalID      string
	ConsoleTenants          []ConsoleTenant
	ConsolePrincipals       []ConsolePrincipal
}

func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		StateFile: ".org/state.json", TemporalAddress: "127.0.0.1:7233", WorkerTemporalAddress: "host.docker.internal:7233",
		TemporalNamespace: "default", TemporalWebBaseURL: "http://127.0.0.1:8080", WorkerBootstrapEndpoint: "http://host.docker.internal:8090/internal/v1/bootstrap/register", KubeContext: "kind-org",
		KubeNamespace: "org-workers", RegistryAllowlist: []string{"ghcr.io"},
		ConsoleAddress: "127.0.0.1:8090", ConsoleTenantID: "tenant-local", ConsoleTenantSlug: "local",
		ConsoleTenantName: "Local Development", ConsolePrincipalID: "local-developer",
	}
	set(&cfg.StateFile, getenv("ORG_STATE_FILE"))
	set(&cfg.TemporalAddress, getenv("ORG_TEMPORAL_ADDRESS"))
	set(&cfg.WorkerTemporalAddress, getenv("ORG_WORKER_TEMPORAL_ADDRESS"))
	set(&cfg.WorkerBootstrapEndpoint, getenv("ORG_WORKER_BOOTSTRAP_ENDPOINT"))
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
	if value := strings.TrimSpace(getenv("ORG_CONSOLE_PRINCIPALS")); value != "" {
		principals, err := decodeConsolePrincipals(value, cfg.ConsolePrincipalID)
		if err != nil {
			return Config{}, err
		}
		cfg.ConsolePrincipals = principals
	} else {
		cfg.ConsolePrincipals = []ConsolePrincipal{{ID: cfg.ConsolePrincipalID, DisplayName: cfg.ConsolePrincipalID}}
	}
	if value := strings.TrimSpace(getenv("ORG_CONSOLE_TENANTS")); value != "" {
		tenants, err := decodeConsoleTenants(value)
		if err != nil {
			return Config{}, err
		}
		cfg.ConsoleTenants = tenants
		cfg.ConsoleTenantID, cfg.ConsoleTenantSlug, cfg.ConsoleTenantName = tenants[0].ID, tenants[0].Slug, tenants[0].DisplayName
	} else {
		cfg.ConsoleTenants = []ConsoleTenant{{ID: cfg.ConsoleTenantID, Slug: cfg.ConsoleTenantSlug, DisplayName: cfg.ConsoleTenantName}}
	}
	if value := strings.TrimSpace(getenv("ORG_REGISTRY_ALLOWLIST")); value != "" {
		cfg.RegistryAllowlist = splitList(value)
	}
	if strings.HasPrefix(cfg.KubeContext, "kind-") && isLoopbackAddress(cfg.WorkerTemporalAddress) {
		return Config{}, errors.New("Worker Temporal address cannot be localhost for a kind cluster")
	}
	if !strings.HasPrefix(cfg.KubeContext, "kind-") && !strings.HasPrefix(strings.ToLower(cfg.WorkerBootstrapEndpoint), "https://") {
		return Config{}, errors.New("production Worker bootstrap endpoint must use HTTPS")
	}
	if len(cfg.RegistryAllowlist) == 0 {
		return Config{}, errors.New("at least one image registry must be allowlisted")
	}
	return cfg, nil
}

func decodeConsolePrincipals(value, authenticatedPrincipalID string) ([]ConsolePrincipal, error) {
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.DisallowUnknownFields()
	var principals []ConsolePrincipal
	if err := decoder.Decode(&principals); err != nil {
		return nil, errors.New("ORG_CONSOLE_PRINCIPALS must be a JSON array with only id and displayName")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("ORG_CONSOLE_PRINCIPALS must contain one JSON value")
	}
	if len(principals) == 0 {
		return nil, errors.New("ORG_CONSOLE_PRINCIPALS must contain at least the authenticated principal")
	}
	ids, includesAuthenticated := map[string]bool{}, false
	for index := range principals {
		principals[index].ID = strings.TrimSpace(principals[index].ID)
		principals[index].DisplayName = strings.TrimSpace(principals[index].DisplayName)
		principal := principals[index]
		if principal.ID == "" || principal.DisplayName == "" || ids[principal.ID] {
			return nil, errors.New("ORG_CONSOLE_PRINCIPALS identities must be non-empty and unique")
		}
		ids[principal.ID] = true
		includesAuthenticated = includesAuthenticated || principal.ID == authenticatedPrincipalID
	}
	if !includesAuthenticated {
		return nil, errors.New("ORG_CONSOLE_PRINCIPALS must include ORG_CONSOLE_PRINCIPAL_ID")
	}
	return principals, nil
}

func decodeConsoleTenants(value string) ([]ConsoleTenant, error) {
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.DisallowUnknownFields()
	var tenants []ConsoleTenant
	if err := decoder.Decode(&tenants); err != nil {
		return nil, errors.New("ORG_CONSOLE_TENANTS must be a JSON array with only id, slug, and displayName")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("ORG_CONSOLE_TENANTS must contain one JSON value")
	}
	if len(tenants) == 0 {
		return nil, errors.New("ORG_CONSOLE_TENANTS must contain at least one authorized Tenant")
	}
	ids, slugs := map[string]bool{}, map[string]bool{}
	for index := range tenants {
		tenants[index].ID = strings.TrimSpace(tenants[index].ID)
		tenants[index].Slug = strings.TrimSpace(tenants[index].Slug)
		tenants[index].DisplayName = strings.TrimSpace(tenants[index].DisplayName)
		tenant := tenants[index]
		if tenant.ID == "" || tenant.Slug == "" || tenant.DisplayName == "" || ids[tenant.ID] || slugs[tenant.Slug] {
			return nil, errors.New("ORG_CONSOLE_TENANTS identities must be non-empty and unique")
		}
		ids[tenant.ID], slugs[tenant.Slug] = true, true
	}
	return tenants, nil
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
