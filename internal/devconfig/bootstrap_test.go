package devconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKindAndTemporalBootstrapFilesMatchRuntimeDefaults(t *testing.T) {
	root := filepath.Join("..", "..")
	kindConfig := read(t, filepath.Join(root, "deploy", "dev", "kind.yaml"))
	makefile := read(t, filepath.Join(root, "Makefile"))
	guide := read(t, filepath.Join(root, "docs", "development.md"))
	preflight := read(t, filepath.Join(root, "scripts", "e2e-preflight.sh"))
	cleanup := read(t, filepath.Join(root, "scripts", "e2e-clean.sh"))
	for _, want := range []string{"kind: Cluster", "kind.x-k8s.io/v1alpha4"} {
		if !strings.Contains(kindConfig, want) {
			t.Errorf("kind config missing %q", want)
		}
	}
	for _, want := range []string{"kind create cluster --name org", "temporal server start-dev", "--port 7233", "--ui-port 8080", "--db-filename .org/temporal.db"} {
		if !strings.Contains(makefile, want) {
			t.Errorf("Makefile missing %q", want)
		}
	}
	for _, want := range []string{"e2e-preflight:", "e2e-local:", "e2e-clean:", "ORG_E2E=1"} {
		if !strings.Contains(makefile, want) {
			t.Errorf("Makefile missing %q", want)
		}
	}
	for _, want := range []string{"parallel-sample-test:", "parallel-sample-image:", "parallel-sample-kind-load:", "parallel-e2e-local:", "TestLocalParallelConfirmationAcceptance", "samples/parallel-confirmation/scripts/build-image.sh"} {
		if !strings.Contains(makefile, want) {
			t.Errorf("Makefile missing %q", want)
		}
	}
	for _, want := range []string{"dynamic-sample-test:", "dynamic-sample-image:", "dynamic-sample-kind-load:", "dynamic-e2e-local:", "TestLocalDynamicDecisionAcceptance", "samples/dynamic-decision/scripts/build-image.sh"} {
		if !strings.Contains(makefile, want) {
			t.Errorf("Makefile missing %q", want)
		}
	}
	for _, want := range []string{"kind-org", "127.0.0.1", "7233", "samples/hello/Dockerfile", "samples/parallel-confirmation/Dockerfile", "samples/dynamic-decision/Dockerfile"} {
		if !strings.Contains(preflight, want) {
			t.Errorf("preflight missing %q", want)
		}
	}
	if strings.Contains(preflight, "generated/org-worker-manifest.json") {
		t.Fatal("E2E preflight still requires generated manifest files")
	}
	for _, want := range []string{"RUN_ID", "org-e2e-", "hello-worker:e2e-"} {
		if !strings.Contains(cleanup, want) {
			t.Errorf("cleanup missing %q", want)
		}
	}
	for _, want := range []string{"kind", "host.docker.internal:7233", "ORG_KUBE_CONTEXT", "ORG_WORKER_TEMPORAL_ADDRESS", "does not build", "parallel-e2e-local", "dynamic-e2e-local"} {
		if !strings.Contains(guide, want) {
			t.Errorf("development guide missing %q", want)
		}
	}
	if strings.Contains(guide, "still needs the `kind` CLI/cluster") {
		t.Error("development guide contains stale local prerequisite status")
	}
}

func TestSamplesUseHostedBootstrapStartupWithoutManifestFiles(t *testing.T) {
	for _, sample := range []string{"hello", "parallel-confirmation", "dynamic-decision"} {
		path := filepath.Join("..", "..", "samples", sample, "cmd", "worker", "main.go")
		text := read(t, path)
		if !strings.Contains(text, "LoadHostedWorkerConfig") || !strings.Contains(text, "RunHostedWorker") || strings.Contains(text, ".Manifest()") || strings.Contains(text, "NewWorkerRuntime") {
			t.Fatalf("sample %s has stale startup path:\n%s", sample, text)
		}
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	value, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(value)
}
