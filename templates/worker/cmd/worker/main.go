package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	worker "example.com/my-worker"
	"github.com/wu8685/org/sdk/orgsdk"
)

func main() {
	cfg, err := orgsdk.LoadHostedWorkerConfig(os.Getenv, os.ReadFile)
	if err != nil {
		log.Fatal(err)
	}
	hostedWorker, err := worker.NewWorker(cfg.Worker.BuildID)
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := orgsdk.RunHostedWorker(ctx, cfg, hostedWorker.Registrations()...); err != nil {
		log.Fatal(err)
	}
}
