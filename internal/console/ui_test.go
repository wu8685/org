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
		"/workers/hello-worker/versions/v1/workflows/HelloWorkflow", "/runs", "/runs/inv-1", "/tenants", "/tenants/studio",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK || !strings.HasPrefix(response.Header().Get("Content-Type"), "text/html") {
			t.Fatalf("GET %s status=%d headers=%v body=%s", path, response.Code, response.Header(), response.Body.String())
		}
		body := response.Body.String()
		for _, want := range []string{"Workers", "Workflows", "Runs", "管理 Tenants", "Alpha Studio", `data-page="`} {
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
	identity := testIdentity()
	identity.AuthorizedTenants = []TenantMembership{
		{TenantID: "tenant-a", TenantSlug: "alpha", DisplayName: "Alpha Studio"},
		{TenantID: "tenant-b", TenantSlug: "beta", DisplayName: "Beta Studio"},
	}
	handler := New(Config{Authenticator: stubAuthenticator{identity: identity}, ControlPlane: &stubControlPlane{}})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/runs/inv-1", nil))
	body := response.Body.String()
	for _, want := range []string{
		`<link rel="icon" href="/assets/org-logo-favicon-v2.svg" type="image/svg+xml">`,
		`class="brand" href="/" aria-label="org Console 首页"`,
		`class="brand-logo brand-logo-full" src="/assets/org-logo-v2.svg"`,
		`class="brand-logo brand-logo-mark" src="/assets/org-logo-mark-v2.svg"`,
		`data-dag-canvas`, `data-dag-list`, `aria-live="polite"`, `data-action-dialog`,
		`data-tenant-switch`, `name="tenantSlug"`, `aria-label="切换当前 Tenant"`, `data-tenant-switch-status`, `aria-label="breadcrumb"`, `Tenant stable ID`,
		`data-contract-readonly`, `data-schema-fields`, `data-worker-dialog`, `data-worker-form`,
		`data-worker-error`, `aria-describedby="worker-name-help worker-name-error"`,
		`data-trigger-payload`, `data-trigger-error`, `data-trigger-schema-reference`, `data-trigger-example`,
		`name="inputFormat"`, `name="description"`, `maxlength="1000"`,
		`<dialog class="dialog" data-worker-dialog`, `<form method="dialog" class="dialog-head">`,
		`<form class="form-grid" data-worker-form>`, `<div class="form-actions">`, `data-dialog-close`,
		`app.css`, `yaml-renderer.js`, `app.js`,
		`data-tenant-dialog`, `data-tenant-form`, `data-tenant-error`, `data-member-dialog`, `data-member-form`, `data-member-error`,
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
	if strings.Contains(body, `class="brand-mark"`) {
		t.Fatal("shell retained the text-only org brand placeholder")
	}
}

func TestConsoleServesAllowlistedEmbeddedBrandAssets(t *testing.T) {
	handler := New(Config{Authenticator: stubAuthenticator{identity: testIdentity()}, ControlPlane: &stubControlPlane{}})
	for _, name := range []string{
		"org-logo-v2.svg", "org-logo-mark-v2.svg", "org-logo-mono-v2.svg", "org-logo-favicon-v2.svg",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/assets/"+name, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("GET /assets/%s status=%d body=%s", name, response.Code, response.Body.String())
		}
		if response.Header().Get("Content-Type") != "image/svg+xml" {
			t.Errorf("GET /assets/%s Content-Type=%q", name, response.Header().Get("Content-Type"))
		}
		if response.Header().Get("Cache-Control") != "public, max-age=300" {
			t.Errorf("GET /assets/%s Cache-Control=%q", name, response.Header().Get("Cache-Control"))
		}
		if !strings.Contains(response.Body.String(), "<svg") {
			t.Errorf("GET /assets/%s did not return SVG", name)
		}
	}

	unknown := httptest.NewRecorder()
	handler.ServeHTTP(unknown, httptest.NewRequest(http.MethodGet, "/assets/org-logo-unapproved.svg", nil))
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown brand asset status=%d body=%s", unknown.Code, unknown.Body.String())
	}
}

func TestConsoleSingleTenantUsesStaticIdentityAndResourceBreadcrumbsEncodeHierarchy(t *testing.T) {
	handler := New(Config{Authenticator: stubAuthenticator{identity: testIdentity()}, ControlPlane: &stubControlPlane{}})

	overview := httptest.NewRecorder()
	handler.ServeHTTP(overview, httptest.NewRequest(http.MethodGet, "/", nil))
	if strings.Contains(overview.Body.String(), `name="tenantSlug"`) || !strings.Contains(overview.Body.String(), `data-tenant-identity`) {
		t.Fatalf("single-Tenant shell must render static identity without selector: %s", overview.Body.String())
	}

	workflow := httptest.NewRecorder()
	handler.ServeHTTP(workflow, httptest.NewRequest(http.MethodGet, "/workers/hello-worker/versions/v1/workflows/HelloWorkflow", nil))
	body := workflow.Body.String()
	ordered := []string{`Tenant: Alpha Studio`, `>Workers<`, `>hello-worker<`, `>Versions<`, `>v1<`, `>Workflows<`, `>HelloWorkflow<`}
	position := -1
	for _, want := range ordered {
		next := strings.Index(body[position+1:], want)
		if next < 0 {
			t.Fatalf("resource breadcrumb missing %q: %s", want, body)
		}
		position += next + 1
	}
}

func TestProgressiveAssetsEncodeDynamicDependenciesGatewayHeadersAndResponsiveLayout(t *testing.T) {
	handler := New(Config{Authenticator: stubAuthenticator{identity: testIdentity()}, ControlPlane: &stubControlPlane{}})
	checks := []struct {
		path  string
		wants []string
	}{
		{"/assets/app.css", []string{"--accent: #2f6feb", "grid-template-columns: 244px", "@media (max-width: 700px)", ".brand-logo-full", ".brand-logo-mark", ".dag-list", ".yaml-view", ".run-status-summary", ".run-failure-panel", ".tenant-card", ".member-actions", "prefers-reduced-motion"}},
		{"/assets/yaml-renderer.js", []string{"YAML display unavailable", "MAX_DEPTH", "MAX_OUTPUT", "Object.keys(value).sort()", "module.exports", "canonicalJSON", "YAML parse error", "custom tags, anchors, aliases, and merge keys are disabled"}},
		{"/assets/app.js", []string{"semanticProjection", "dependencies", "runtimeNodeId", "publishOperationKey", `headers: {"Idempotency-Key": publishOperationKey}`, "Idempotency-Key", "If-Match", "delivery-unknown", "visibilitychange", "buildSchemaFields", "inputSchema", "requiredPermission", "yamlView", "navigator.clipboard.writeText", `aria-live`, "payloadCodec.parse", "exampleFromSchema", "triggerError", "/api/v1/session/tenant", "tenantSlug", "tenantSwitchStatus", "当前 Tenant 还没有 WorkerVersion", "当前 Tenant 没有 Ready WorkerVersion", "当前 Tenant 的筛选条件下没有 Run", "lastRunsETag", "semanticStatus", "runStatusSummary", "runFailurePanel", "errorSummary", "Failure code", "Run failed", `role: "alert"`, "Waiting for user", "Cancelled", "renderTenants", "renderTenant", "/api/v1/tenants", "tenantDetailETag", "openTenantDialog", "openMemberDialog", "tenant:member:manage"}},
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
			for _, forbidden := range []string{`tenantId:`, `?tenantId=`} {
				if strings.Contains(response.Body.String(), forbidden) {
					t.Fatalf("GET %s allows client-controlled Tenant routing %q", check.path, forbidden)
				}
			}
			for _, want := range []string{`tenantDialog.addEventListener("close"`, `memberDialog.addEventListener("close"`, `tenantDialog.close(); location.reload();`, `location.href = "/tenants"`} {
				if !strings.Contains(response.Body.String(), want) {
					t.Fatalf("GET %s missing Tenant dialog/fallback behavior %q", check.path, want)
				}
			}
		}
		if check.path == "/assets/app.css" && strings.Contains(response.Body.String(), ".tenant-stable-id { display: none; }") {
			t.Fatal("mobile layout must keep the current Tenant stable identifier visible")
		}
	}
}
