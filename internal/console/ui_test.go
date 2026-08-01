package console

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConsoleRoutesRenderAuthenticatedServerShellWithoutUnsupportedProductConcepts(t *testing.T) {
	handler := New(Config{Authenticator: stubAuthenticator{identity: testIdentity()}, ControlPlane: &stubControlPlane{}})
	for _, path := range []string{
		"/", "/workers", "/workers/hello-worker", "/workers/hello-worker/versions/v1", "/workflows",
		"/workers/hello-worker/versions/v1/workflows/HelloWorkflow", "/runs", "/runs/inv-1",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK || !strings.HasPrefix(response.Header().Get("Content-Type"), "text/html") {
			t.Fatalf("GET %s status=%d headers=%v body=%s", path, response.Code, response.Header(), response.Body.String())
		}
		body := response.Body.String()
		for _, want := range []string{"Workers", "Workflows", "Runs", "Alpha Studio", `data-page="`} {
			if !strings.Contains(body, want) {
				t.Fatalf("GET %s missing %q", path, want)
			}
		}
		for _, forbidden := range []string{"Routines", "scope", "Temporal Namespace", "Kubernetes Namespace", "Task Queue"} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("GET %s leaked unsupported concept %q", path, forbidden)
			}
		}
	}

	routines := httptest.NewRecorder()
	handler.ServeHTTP(routines, httptest.NewRequest(http.MethodGet, "/routines", nil))
	if routines.Code != http.StatusNotFound {
		t.Fatalf("unsupported UI route status=%d body=%s", routines.Code, routines.Body.String())
	}
}

func TestConsoleShellContainsReadonlyContractRuntimeDAGAndAccessibleMobileList(t *testing.T) {
	handler := New(Config{Authenticator: stubAuthenticator{identity: testIdentity()}, ControlPlane: &stubControlPlane{}})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/runs/inv-1", nil))
	body := response.Body.String()
	for _, want := range []string{
		`data-dag-canvas`, `data-dag-list`, `aria-live="polite"`, `data-action-dialog`,
		`data-contract-readonly`, `data-schema-fields`, `data-worker-dialog`, `data-worker-form`,
		`data-worker-error`, `aria-describedby="worker-name-help worker-name-error"`,
		`data-trigger-payload`, `data-trigger-error`, `data-trigger-schema-reference`, `data-trigger-example`,
		`name="inputFormat"`, `name="description"`, `maxlength="1000"`,
		`<dialog class="dialog" data-worker-dialog`, `<form method="dialog" class="dialog-head">`,
		`<form class="form-grid" data-worker-form>`, `<div class="form-actions">`, `data-dialog-close`,
		`app.css`, `yaml-renderer.js`, `app.js`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("shell missing %q: %s", want, body)
		}
	}
	if strings.Index(body, `yaml-renderer.js`) > strings.Index(body, `app.js`) {
		t.Fatal("YAML renderer must load before the Console application")
	}
	for _, forbidden := range []string{"dag-branch-a", "approval-node", "finish-node", "contract-textarea", "routine-modal", `type="file"`, `name="manifest"`, `name="repository"`, `name="branch"`, `name="commit"`, `name="ciReference"`, `data-trigger-schema-fields`, `name="input" required`} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("shell retained fixed/reference-only behavior %q", forbidden)
		}
	}
}

func TestProgressiveAssetsEncodeDynamicDependenciesGatewayHeadersAndResponsiveLayout(t *testing.T) {
	handler := New(Config{Authenticator: stubAuthenticator{identity: testIdentity()}, ControlPlane: &stubControlPlane{}})
	checks := []struct {
		path  string
		wants []string
	}{
		{"/assets/app.css", []string{"--accent: #2f6feb", "grid-template-columns: 244px", "@media (max-width: 700px)", ".dag-list", ".yaml-view", "prefers-reduced-motion"}},
		{"/assets/yaml-renderer.js", []string{"YAML display unavailable", "MAX_DEPTH", "MAX_OUTPUT", "Object.keys(value).sort()", "module.exports", "canonicalJSON", "YAML parse error", "custom tags, anchors, aliases, and merge keys are disabled"}},
		{"/assets/app.js", []string{"semanticProjection", "dependencies", "runtimeNodeId", "publishOperationKey", `headers: {"Idempotency-Key": publishOperationKey}`, "Idempotency-Key", "If-Match", "delivery-unknown", "visibilitychange", "buildSchemaFields", "inputSchema", "requiredPermission", "yamlView", "navigator.clipboard.writeText", `aria-live`, "payloadCodec.parse", "exampleFromSchema", "triggerError"}},
	}
	for _, check := range checks {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, check.path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s status=%d body=%s", check.path, response.Code, response.Body.String())
		}
		for _, want := range check.wants {
			if !strings.Contains(response.Body.String(), want) {
				t.Fatalf("GET %s missing %q", check.path, want)
			}
		}
		for _, forbidden := range []string{"/signal", "Event History", "Routines"} {
			if strings.Contains(response.Body.String(), forbidden) {
				t.Fatalf("GET %s contains forbidden behavior %q", check.path, forbidden)
			}
		}
		if check.path == "/assets/app.js" {
			for _, want := range []string{`workerDialog.showModal()`, `workerName.focus()`, `workerDialog.addEventListener("close"`, `workerError`} {
				if !strings.Contains(response.Body.String(), want) {
					t.Fatalf("GET %s missing accessible Worker dialog behavior %q", check.path, want)
				}
			}
			for _, forbidden := range []string{`window.prompt("Worker name")`, "elements.repository", "elements.branch", "elements.commit", "elements.ciReference", "source:"} {
				if strings.Contains(response.Body.String(), forbidden) {
					t.Fatalf("GET %s contains obsolete public publish/Worker input %q", check.path, forbidden)
				}
			}
			for _, forbidden := range []string{`JSON.stringify(version.versionConfig`, `JSON.stringify(version.contract`, `JSON.stringify(workflow.inputSchema`, `.innerHTML`} {
				if strings.Contains(response.Body.String(), forbidden) {
					t.Fatalf("GET %s contains unsafe or obsolete JSON display %q", check.path, forbidden)
				}
			}
			for _, forbidden := range []string{`buildSchemaFields(workflowContract.inputSchema`, `JSON.parse(triggerForm.elements.input.value)`, `data-trigger-schema-property`} {
				if strings.Contains(response.Body.String(), forbidden) {
					t.Fatalf("GET %s retains schema-derived Trigger fields or JSON-only parsing %q", check.path, forbidden)
				}
			}
		}
	}
}
