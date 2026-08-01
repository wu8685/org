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
	if err := ensureDevelopmentTenant(store, cfg); err != nil {
		log.Fatal(err)
	}
	executor, err := temporalplatform.Dial(temporalplatform.Config{Address: cfg.TemporalAddress, Namespace: cfg.TemporalNamespace})
	if err != nil {
		log.Fatalf("connect local execution service: %v", err)
	}
	defer executor.Close()
	cluster := kube.New(kube.Config{
		Namespace: cfg.KubeNamespace, Context: cfg.KubeContext, Kubeconfig: cfg.Kubeconfig,
		WorkerTemporalAddress: cfg.WorkerTemporalAddress, TemporalNamespace: cfg.TemporalNamespace,
	}, nil)
	controlPlane := service.New(service.Config{
		RegistryAllowlist: cfg.RegistryAllowlist, TemporalWebBaseURL: cfg.TemporalWebBaseURL, TemporalNamespace: cfg.TemporalNamespace,
		BootstrapEndpoint: cfg.WorkerBootstrapEndpoint, BootstrapVerifier: service.StrictBootstrapWorkloadVerifier{},
	}, store, cluster, executor)
	consoleHandler := console.New(console.Config{
		Authenticator: console.StaticAuthenticator{Identity: developmentIdentity(cfg, randomToken())},
		ControlPlane:  controlPlane,
	})
	mux := http.NewServeMux()
	mux.Handle("/internal/v1/bootstrap/register", service.NewBootstrapRegistrationHandler(controlPlane, kube.NewBootstrapEvidenceResolver(kube.Config{Namespace: cfg.KubeNamespace, Context: cfg.KubeContext, Kubeconfig: cfg.Kubeconfig}, nil)))
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
	if _, ok := store.Tenant(cfg.ConsoleTenantID); ok {
		return nil
	}
	now := time.Now().UTC()
	return store.SaveTenant(domain.Tenant{
		ID: cfg.ConsoleTenantID, Slug: cfg.ConsoleTenantSlug, DisplayName: cfg.ConsoleTenantName,
		Status: domain.TenantActive, QuotaPolicy: domain.DefaultTenantQuotaPolicy(), CreatedAt: now, UpdatedAt: now,
	})
}

func developmentIdentity(cfg config.Config, csrfToken string) console.Identity {
	permissions := map[string]bool{
		service.PermissionWorkerRead: true, service.PermissionWorkerCreate: true, service.PermissionWorkerDeploy: true,
		service.PermissionWorkerVersionUpdate: true, service.PermissionRunStart: true, service.PermissionRunRead: true,
		service.PermissionRunSignal: true, service.PermissionRunQuery: true, service.PermissionRunCancel: true,
		service.PermissionDiagnosticsRead: true, service.PermissionAuditRead: true, service.PermissionTenantAdmin: true,
		"run:action:confirm": true,
	}
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
