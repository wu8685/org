package main

import (
	"fmt"
	"log"
	"os"

	hello "github.com/wu8685/org-sample-hello"
)

func main() {
	worker, err := hello.NewWorker("manifest")
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
	manifest = append(manifest, '\n')
	if err := os.WriteFile("generated/org-worker-manifest.json", manifest, 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Println("MANIFEST_DIGEST=" + digest)
}
