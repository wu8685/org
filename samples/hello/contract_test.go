package hello

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wu8685/org/sdk/orgsdk"
)

func TestGeneratedManifestMatchesHelloDefinition(t *testing.T) {
	worker, err := NewWorker("manifest")
	if err != nil {
		t.Fatal(err)
	}
	want, digest, err := worker.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("generated/org-worker-manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.TrimSpace(got), want) {
		t.Fatalf("generated manifest is stale; run go run ./cmd/generate-manifest\ndigest=%s", digest)
	}
	var manifest orgsdk.Manifest
	if err := json.Unmarshal(got, &manifest); err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(got, &raw); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"scope", "name", "workerName", "description", "tenantId"} {
		if _, exists := raw[forbidden]; exists {
			t.Fatalf("manifest must not expose top-level %q", forbidden)
		}
	}
	if manifest.ContractVersion != orgsdk.ContractVersion || len(manifest.Workflows) != 1 || len(manifest.Activities) != 2 {
		t.Fatalf("manifest = %#v", manifest)
	}
	wf := manifest.Workflows[0]
	if wf.Name != WorkflowName || wf.VersioningBehavior != "pinned" || wf.ProjectionQuery != orgsdk.ReservedProjectionQuery || len(wf.NodeTemplates) != 3 {
		t.Fatalf("Workflow metadata = %#v", wf)
	}
	if manifest.Activities[0].Name != prepareGreetingActivityID || manifest.Activities[1].Name != composeGreetingActivityID {
		t.Fatalf("Activity metadata = %#v", manifest.Activities)
	}
}

func TestProductionSampleSourceDoesNotImportRawTemporalSDK(t *testing.T) {
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

func TestImageFilesStaySmallAndReadable(t *testing.T) {
	checks := map[string][]string{
		"Dockerfile":             {"COPY go.mod go.sum", "COPY sdk ./sdk", "COPY samples/hello", "org.opencontainers.image.version", "org.opencontainers.image.revision", "USER 65532:65532", "ENTRYPOINT"},
		"scripts/build-image.sh": {"repo_root=", "--file", "docker build", "VERSION=", "COMMIT=", "--kind-load", "kind-load.sh"},
		"scripts/kind-load.sh":   {"kind load docker-image", "images tag", "crictl inspecti", "IMAGE_DIGEST="},
	}
	for file, wants := range checks {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range wants {
			if !strings.Contains(string(b), want) {
				t.Errorf("%s missing %q", file, want)
			}
		}
	}
}
