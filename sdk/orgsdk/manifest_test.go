package orgsdk

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestGenerateManifestIsCanonicalAndContainsDynamicContract(t *testing.T) {
	definition := graphDefinition()
	first, firstDigest, err := GenerateManifest("DynamicWorkflow", definition)
	if err != nil {
		t.Fatal(err)
	}
	second, secondDigest, err := GenerateManifest("DynamicWorkflow", definition)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) || firstDigest != secondDigest {
		t.Fatalf("manifest is not canonical: %q/%q", firstDigest, secondDigest)
	}
	if !strings.HasPrefix(firstDigest, "sha256:") {
		t.Fatalf("digest = %q", firstDigest)
	}
	var manifest Manifest
	if err := json.Unmarshal(first, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.ContractVersion != ContractVersion || manifest.DynamicNodeIDVersion != DynamicNodeIDVersion || manifest.ProjectionEventVersion != ProjectionEventVersion {
		t.Fatalf("manifest versions = %#v", manifest)
	}
	if len(manifest.Workflows) != 1 || manifest.Workflows[0].Name != "DynamicWorkflow" || manifest.Workflows[0].ProjectionQuery != ReservedProjectionQuery || len(manifest.Workflows[0].NodeTemplates) != len(definition.Templates) {
		t.Fatalf("workflow manifest = %#v", manifest.Workflows)
	}
	text := string(first)
	for _, forbidden := range []string{"tenantId", "scope", "workerName", "taskQueue", "signalName"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("manifest leaked %q: %s", forbidden, text)
		}
	}
}

func TestGenerateManifestRejectsReservedTemplatePrefix(t *testing.T) {
	definition := graphDefinition()
	definition.Templates[0].ID = "org.sdk/hidden"
	if _, _, err := GenerateManifest("DynamicWorkflow", definition); err == nil {
		t.Fatal("reserved SDK template prefix was accepted")
	}
}

func TestTypedDefinitionGeneratesWorkflowAndActivitySchemasWithoutMetadataDuplication(t *testing.T) {
	type request struct {
		Name  string `json:"name"`
		Count int    `json:"count,omitempty"`
	}
	type response struct {
		Greeting string `json:"greeting"`
	}
	policy := ActivityPolicy{SideEffect: SideEffectNone, Retry: RetryPolicy{MaximumAttempts: 1, StartToCloseTimeout: time.Second}}
	prepare := NewActivity("prepare", policy, func(_ ActivityContext, input request) (response, error) {
		return response{Greeting: input.Name}, nil
	})
	definition := NewDefinition[request, response](
		"typed", []NodeTemplate{ActivityNode(prepare, "Prepare", CardinalitySingleton)},
		RuntimeBounds{MaxInstancesPerFanOut: 1, MaxRuntimeNodes: 2, MaxProjectionBytes: 8192},
	)
	manifestJSON, _, err := GenerateManifest("TypedWorkflow", definition)
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Workflows) != 1 || len(manifest.Workflows[0].InputSchema) == 0 || len(manifest.Workflows[0].OutputSchema) == 0 {
		t.Fatalf("workflow schemas = %#v", manifest.Workflows)
	}
	if len(manifest.Activities) != 1 || manifest.Activities[0].Name != "prepare" || len(manifest.Activities[0].InputSchema) == 0 || len(manifest.Activities[0].OutputSchema) == 0 || manifest.Activities[0].Policy.SideEffect != SideEffectNone {
		t.Fatalf("activity manifests = %#v", manifest.Activities)
	}
	var inputSchema map[string]any
	if err := json.Unmarshal(manifest.Workflows[0].InputSchema, &inputSchema); err != nil {
		t.Fatal(err)
	}
	required, _ := inputSchema["required"].([]any)
	if len(required) != 1 || required[0] != "name" {
		t.Fatalf("input schema = %#v", inputSchema)
	}
}
