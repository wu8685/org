package parallelconfirmation

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wu8685/org/sdk/orgsdk"
)

func TestGeneratedManifestMatchesDefinitionAndActionContract(t *testing.T) {
	worker, err := NewWorker("manifest")
	if err != nil {
		t.Fatal(err)
	}
	want, digest, err := worker.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	got, gotDigest, err := worker.Manifest()
	if err != nil || !bytes.Equal(got, want) || gotDigest != digest {
		t.Fatalf("in-memory contract is not canonical: digest=%s second=%s error=%v", digest, gotDigest, err)
	}
	var manifest orgsdk.Manifest
	if err := json.Unmarshal(got, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Workflows) != 1 || len(manifest.Workflows[0].Actions) != 1 || len(manifest.Activities) != 3 {
		t.Fatalf("manifest = %#v", manifest)
	}
	action := manifest.Workflows[0].Actions[0]
	if action.Name != "confirm" || action.RequiredPermission != "run:action:confirm" || !bytes.Equal(action.InputSchema, json.RawMessage(`{"type":"object","additionalProperties":false}`)) {
		t.Fatalf("action = %#v", action)
	}
	workflow := manifest.Workflows[0]
	if workflow.Name != WorkflowName || workflow.VersioningBehavior != "pinned" || workflow.ProjectionQuery != orgsdk.ReservedProjectionQuery || len(workflow.NodeTemplates) != 5 {
		t.Fatalf("workflow = %#v", workflow)
	}
}

func TestProductionSourceDoesNotImportRawTemporalSDK(t *testing.T) {
	err := filepath.WalkDir(".", func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(contents), "go.temporal.io/") {
			t.Errorf("%s imports raw Temporal SDK", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestImageAndKindFilesExposeReproducibleBuildContract(t *testing.T) {
	checks := map[string][]string{
		"Makefile":               {"test:", "vet:", "verify:", "image:", "push:", "kind-load:"},
		"Dockerfile":             {"COPY go.mod go.sum", "COPY . .", "org.opencontainers.image.version", "org.opencontainers.image.revision", "USER 65532:65532", "ENTRYPOINT"},
		"scripts/build-image.sh": {"sample_dir=", "--file", "docker build", "VERSION=", "COMMIT=", "--kind-load", "--push", `"$sample_dir"`},
		"scripts/push-image.sh":  {"docker push", "digest:", "IMAGE_DIGEST="},
		"scripts/kind-load.sh":   {"kind load docker-image", "images tag", "crictl inspecti", "IMAGE_DIGEST="},
	}
	for file, wants := range checks {
		contents, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range wants {
			if !strings.Contains(string(contents), want) {
				t.Errorf("%s missing %q", file, want)
			}
		}
	}
}
