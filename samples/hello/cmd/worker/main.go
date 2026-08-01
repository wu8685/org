package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	hello "github.com/wu8685/org-sample-hello"
	"github.com/wu8685/org/sdk/orgsdk"
)

func main() {
	cfg, err := hello.LoadConfig(os.Getenv)
	if err != nil {
		log.Fatal(err)
	}
	worker, err := hello.NewWorker(cfg.TemporalWorkerBuildID)
	if err != nil {
		log.Fatal(err)
	}
	_, manifestDigest, err := worker.Manifest()
	if err != nil {
		log.Fatal(err)
	}
	runtime, err := orgsdk.NewWorkerRuntime(orgsdk.WorkerConfig{
		TemporalAddress: cfg.TemporalAddress, TemporalNamespace: cfg.TemporalNamespace,
		TaskQueue: cfg.TemporalTaskQueue, DeploymentName: cfg.TemporalWorkerDeployment,
		BuildID: cfg.TemporalWorkerBuildID, ManifestDigest: manifestDigest,
	}, worker.Registrations()...)
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runtime.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
