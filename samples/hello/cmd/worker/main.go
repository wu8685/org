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
	cfg, err := orgsdk.LoadHostedWorkerConfig(os.Getenv, os.ReadFile)
	if err != nil {
		log.Fatal(err)
	}
	worker, err := hello.NewWorker(cfg.Worker.BuildID)
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := orgsdk.RunHostedWorker(ctx, cfg, worker.Registrations()...); err != nil {
		log.Fatal(err)
	}
}
