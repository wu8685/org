package main

import (
	"fmt"
	"log"
	"os"

	parallelconfirmation "github.com/wu8685/org-sample-parallel-confirmation"
)

func main() {
	worker, err := parallelconfirmation.NewWorker("manifest")
	if err != nil {
		log.Fatal(err)
	}
	manifest, digest, err := worker.Manifest()
	if err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll("generated", 0o755); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile("generated/org-worker-manifest.json", append(manifest, '\n'), 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Println("MANIFEST_DIGEST=" + digest)
}
