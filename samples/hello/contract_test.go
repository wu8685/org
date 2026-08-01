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
	got, gotDigest, err := worker.Manifest()
	if err != nil || !bytes.Equal(got, want) || gotDigest != digest {
		t.Fatalf("in-memory contract is not canonical: digest=%s second=%s error=%v", digest, gotDigest, err)
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
		"Makefile":               {"test:", "vet:", "verify:", "image:", "push:", "kind-load:"},
		"Dockerfile":             {"COPY go.mod go.sum", "COPY . .", "org.opencontainers.image.version", "org.opencontainers.image.revision", "USER 65532:65532", "ENTRYPOINT"},
		"scripts/build-image.sh": {"sample_dir=", "--file", "docker build", "VERSION=", "COMMIT=", "--kind-load", "--push", `"$sample_dir"`},
		"scripts/push-image.sh":  {"docker push", "digest:", "IMAGE_DIGEST="},
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
