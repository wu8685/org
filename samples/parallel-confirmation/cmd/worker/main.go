package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	parallelconfirmation "github.com/wu8685/org-sample-parallel-confirmation"
	"github.com/wu8685/org/sdk/orgsdk"
)

func main() {
	cfg, err := parallelconfirmation.LoadConfig(os.Getenv)
	if err != nil {
		log.Fatal(err)
	}
	worker, err := parallelconfirmation.NewWorker(cfg.TemporalWorkerBuildID)
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
