package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/wu8685/org/internal/config"
	"github.com/wu8685/org/internal/console"
	"github.com/wu8685/org/internal/domain"
	"github.com/wu8685/org/internal/platform/kube"
	temporalplatform "github.com/wu8685/org/internal/platform/temporal"
	"github.com/wu8685/org/internal/service"
)

type tenantStore interface {
	Tenant(string) (domain.Tenant, bool)
	SaveTenant(domain.Tenant) error
	CommitTenantCreation(domain.Tenant, domain.TenantMember, domain.AuditRecord) error
	CommitTenantMember(string, domain.TenantMember, int64, domain.AuditRecord) error
}

func main() {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		log.Fatal(err)
	}
	if err := requireLoopback(cfg.ConsoleAddress); err != nil {
		log.Fatal(err)
	}
	store, err := service.NewFileStore(cfg.StateFile)
	if err != nil {
		log.Fatal(err)
	}
	if err := ensureDevelopmentTenants(store, cfg); err != nil {
		log.Fatal(err)
	}
	executor, err := temporalplatform.Dial(temporalplatform.Config{Address: cfg.TemporalAddress, Namespace: cfg.TemporalNamespace})
	if err != nil {
		log.Fatalf("connect local execution service: %v", err)
	}
	defer executor.Close()
	kubeAPI, err := kube.NewAPI(cfg.KubeContext, cfg.Kubeconfig)
	if err != nil {
		log.Fatalf("connect Kubernetes API: %v", err)
	}
	cluster := kube.New(kube.Config{
		Namespace: cfg.KubeNamespace, Context: cfg.KubeContext, Kubeconfig: cfg.Kubeconfig,
		WorkerTemporalAddress: cfg.WorkerTemporalAddress, TemporalNamespace: cfg.TemporalNamespace,
	}, kubeAPI)
	controlPlane := service.New(service.Config{
		RegistryAllowlist: cfg.RegistryAllowlist, TemporalWebBaseURL: cfg.TemporalWebBaseURL, TemporalNamespace: cfg.TemporalNamespace,
		BootstrapEndpoint: cfg.WorkerBootstrapEndpoint, BootstrapVerifier: service.StrictBootstrapWorkloadVerifier{},
		PrincipalDirectory: developmentPrincipalDirectory(cfg),
	}, store, cluster, executor)
	promotionCtx, cancelPromotions := context.WithCancel(context.Background())
	if err := controlPlane.StartBootstrapPromotionController(promotionCtx); err != nil {
		log.Fatal(err)
	}
	if err := controlPlane.StartInvocationReconciler(promotionCtx); err != nil {
		log.Fatal(err)
	}
	defer func() {
		cancelPromotions()
		if err := controlPlane.WaitBootstrapPromotionController(); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("bootstrap promotion controller stopped: %v", err)
		}
		if err := controlPlane.WaitInvocationReconciler(); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("invocation reconciler stopped: %v", err)
		}
	}()
	authenticator, err := console.NewSessionAuthenticator(console.SessionAuthenticatorConfig{
		PrincipalID: cfg.ConsolePrincipalID, SessionKey: "local-development:" + cfg.ConsolePrincipalID,
		AuthenticationMethod: "local-development", CSRFToken: randomToken(), DefaultTenantID: cfg.ConsoleTenantID,
		Directory: store, SelectionStore: store,
	})
	if err != nil {
		log.Fatal(err)
	}
	consoleHandler := console.New(console.Config{
		Authenticator: authenticator,
		ControlPlane:  controlPlane,
	})
	mux := http.NewServeMux()
	mux.Handle("/internal/v1/bootstrap/register", service.NewBootstrapRegistrationHandler(controlPlane, kube.NewBootstrapEvidenceResolver(kube.Config{Namespace: cfg.KubeNamespace, Context: cfg.KubeContext, Kubeconfig: cfg.Kubeconfig}, kubeAPI, nil)))
	mux.Handle("/", consoleHandler)
	handler := http.Handler(mux)
	server := &http.Server{Addr: cfg.ConsoleAddress, Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}

	shutdownSignals := make(chan os.Signal, 1)
	signal.Notify(shutdownSignals, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-shutdownSignals
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()
	log.Printf("org Console listening on http://%s", cfg.ConsoleAddress)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func ensureDevelopmentTenant(store tenantStore, cfg config.Config) error {
	return ensureDevelopmentTenantOwner(store, cfg, config.ConsoleTenant{ID: cfg.ConsoleTenantID, Slug: cfg.ConsoleTenantSlug, DisplayName: cfg.ConsoleTenantName})
}

func ensureDevelopmentTenants(store tenantStore, cfg config.Config) error {
	for _, configured := range cfg.ConsoleTenants {
		if err := ensureDevelopmentTenantOwner(store, cfg, configured); err != nil {
			return err
		}
	}
	return nil
}

func ensureDevelopmentTenantOwner(store tenantStore, cfg config.Config, configured config.ConsoleTenant) error {
	now := time.Now().UTC()
	principalName := cfg.ConsolePrincipalID
	for _, principal := range cfg.ConsolePrincipals {
		if principal.ID == cfg.ConsolePrincipalID {
			principalName = principal.DisplayName
			break
		}
	}
	owner := domain.TenantMember{TenantID: configured.ID, PrincipalID: cfg.ConsolePrincipalID, PrincipalDisplayName: principalName, Role: domain.TenantRoleOwner, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if _, ok := store.Tenant(configured.ID); ok {
		return store.CommitTenantMember(configured.ID, owner, 0, domain.AuditRecord{ID: "bootstrap-member:" + configured.ID + ":" + cfg.ConsolePrincipalID, TenantID: configured.ID, PrincipalID: cfg.ConsolePrincipalID, Action: "tenant.member.bootstrap", CreatedAt: now})
	}
	tenant := domain.Tenant{ID: configured.ID, Slug: configured.Slug, DisplayName: configured.DisplayName, Status: domain.TenantActive, QuotaPolicy: domain.DefaultTenantQuotaPolicy(), Revision: 1, CreatedAt: now, UpdatedAt: now}
	return store.CommitTenantCreation(tenant, owner, domain.AuditRecord{ID: "bootstrap-tenant:" + configured.ID, TenantID: configured.ID, PrincipalID: cfg.ConsolePrincipalID, Action: "tenant.bootstrap", CreatedAt: now})
}

func developmentPrincipalDirectory(cfg config.Config) service.PrincipalDirectory {
	principals := make([]service.Principal, 0, len(cfg.ConsolePrincipals))
	for _, principal := range cfg.ConsolePrincipals {
		principals = append(principals, service.Principal{ID: principal.ID, DisplayName: principal.DisplayName})
	}
	return service.NewStaticPrincipalDirectory(principals...)
}

func developmentMemberships(cfg config.Config) []console.TenantMembership {
	memberships := make([]console.TenantMembership, 0, len(cfg.ConsoleTenants))
	for _, tenant := range cfg.ConsoleTenants {
		memberships = append(memberships, console.TenantMembership{
			TenantID: tenant.ID, TenantSlug: tenant.Slug, DisplayName: tenant.DisplayName, Permissions: developmentPermissions(),
		})
	}
	return memberships
}

func developmentPermissions() map[string]bool {
	return domain.TenantRolePermissions(domain.TenantRoleOwner)
}

func developmentIdentity(cfg config.Config, csrfToken string) console.Identity {
	permissions := developmentPermissions()
	return console.Identity{
		Auth: service.AuthenticatedContext{
			PrincipalID: cfg.ConsolePrincipalID, TenantID: cfg.ConsoleTenantID, TenantSlug: cfg.ConsoleTenantSlug,
			AuthenticationMethod: "local-development", Permissions: permissions,
		},
		TenantDisplayName: cfg.ConsoleTenantName, CSRFToken: csrfToken,
	}
}

func requireLoopback(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return errors.New("console address must include host and port")
	}
	if host != "localhost" && !net.ParseIP(host).IsLoopback() {
		return errors.New("local development Console must bind to a loopback address")
	}
	return nil
}

func randomToken() string {
	var material [24]byte
	if _, err := rand.Read(material[:]); err != nil {
		return "csrf-unavailable"
	}
	return hex.EncodeToString(material[:])
}
