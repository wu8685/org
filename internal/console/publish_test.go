package console

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wu8685/org/internal/domain"
	"github.com/wu8685/org/internal/service"
)

func TestPublishWorkerVersionReturns202AndPollableOperation(t *testing.T) {
	published := make(chan domain.WorkerVersionRequest, 1)
	backend := &stubControlPlane{published: published, version: consoleVersion(time.Now().UTC())}
	handler := New(Config{Authenticator: stubAuthenticator{identity: testIdentity()}, ControlPlane: backend})
	sessionResponse := httptest.NewRecorder()
	handler.ServeHTTP(sessionResponse, httptest.NewRequest(http.MethodGet, "/api/v1/session", nil))
	var session struct {
		Session struct {
			CSRFToken string `json:"csrfToken"`
		} `json:"session"`
	}
	if sessionResponse.Code != http.StatusOK || json.Unmarshal(sessionResponse.Body.Bytes(), &session) != nil || session.Session.CSRFToken == "" {
		t.Fatalf("session status=%d body=%s", sessionResponse.Code, sessionResponse.Body.String())
	}
	body := `{
      "version":"v1",
      "description":"First release",
      "image":"registry.example.com/hello@sha256:` + strings.Repeat("a", 64) + `",
      "versionConfig":{"region":"local","provider":{"secretRef":"provider-token"}},
      "runtime":{"cpu":"100m","memory":"128Mi","environment":[]}
    }`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/workers/hello-worker/versions", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", session.Session.CSRFToken)
	request.Header.Set("Idempotency-Key", "publish-v1")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || response.Header().Get("Location") == "" {
		t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
	select {
	case got := <-published:
		if got.WorkerName != "hello-worker" || got.Version != "v1" || got.Description != "First release" || got.ManifestDigest != "" || len(got.Metadata.Workflows) != 0 || !strings.Contains(string(got.VersionConfig), `"region":"local"`) {
			t.Fatalf("publish request = %#v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("publish did not reach control plane")
	}

	location := response.Header().Get("Location")
	deadline := time.Now().Add(time.Second)
	for {
		poll := httptest.NewRecorder()
		handler.ServeHTTP(poll, httptest.NewRequest(http.MethodGet, location, nil))
		if poll.Code == http.StatusOK && strings.Contains(poll.Body.String(), `"state":"succeeded"`) && strings.Contains(poll.Body.String(), `"workerVersion":`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("operation did not complete: status=%d body=%s", poll.Code, poll.Body.String())
		}
		time.Sleep(time.Millisecond)
	}
}

func TestPublishIdempotencyReturnsSameOperationAndRunsBackendOnce(t *testing.T) {
	published := make(chan domain.WorkerVersionRequest, 2)
	handler := New(Config{Authenticator: stubAuthenticator{identity: testIdentity()}, ControlPlane: &stubControlPlane{published: published}})
	body := publishBody(`{"region":"local","nested":{"b":2,"a":1}}`)

	first := publishRequest(body, "publish-same")
	firstResponse := httptest.NewRecorder()
	handler.ServeHTTP(firstResponse, first)
	secondBody := strings.Replace(body, `"versionConfig":{"nested":{"a":1,"b":2},"region":"local"}`, `"versionConfig": { "region": "local", "nested": { "b": 2, "a": 1 } }`, 1)
	second := publishRequest(secondBody, "publish-same")
	secondResponse := httptest.NewRecorder()
	handler.ServeHTTP(secondResponse, second)

	if firstResponse.Code != http.StatusAccepted || secondResponse.Code != http.StatusAccepted || firstResponse.Header().Get("Location") == "" || firstResponse.Header().Get("Location") != secondResponse.Header().Get("Location") {
		t.Fatalf("first=%d/%q second=%d/%q bodies=%s / %s", firstResponse.Code, firstResponse.Header().Get("Location"), secondResponse.Code, secondResponse.Header().Get("Location"), firstResponse.Body.String(), secondResponse.Body.String())
	}
	select {
	case <-published:
	case <-time.After(time.Second):
		t.Fatal("first publish did not reach backend")
	}
	select {
	case duplicate := <-published:
		t.Fatalf("duplicate reached backend: %#v", duplicate)
	case <-time.After(25 * time.Millisecond):
	}
}

func TestPublishIdempotencyRejectsConflictingPayload(t *testing.T) {
	published := make(chan domain.WorkerVersionRequest, 2)
	handler := New(Config{Authenticator: stubAuthenticator{identity: testIdentity()}, ControlPlane: &stubControlPlane{published: published}})
	firstResponse := httptest.NewRecorder()
	handler.ServeHTTP(firstResponse, publishRequest(publishBody(`{"region":"local"}`), "publish-conflict"))
	conflictResponse := httptest.NewRecorder()
	handler.ServeHTTP(conflictResponse, publishRequest(strings.Replace(publishBody(`{"region":"local"}`), "First release", "Changed release", 1), "publish-conflict"))
	if firstResponse.Code != http.StatusAccepted || conflictResponse.Code != http.StatusConflict || !strings.Contains(conflictResponse.Body.String(), `"code":"idempotency_conflict"`) || !strings.Contains(conflictResponse.Body.String(), "different publish request") {
		t.Fatalf("first=%d conflict=%d body=%s", firstResponse.Code, conflictResponse.Code, conflictResponse.Body.String())
	}
	select {
	case <-published:
	case <-time.After(time.Second):
		t.Fatal("first publish did not reach backend")
	}
	select {
	case duplicate := <-published:
		t.Fatalf("conflicting publish reached backend: %#v", duplicate)
	case <-time.After(25 * time.Millisecond):
	}
}

func TestPublishOperationExplainsExistingImmutableVersion(t *testing.T) {
	handler := New(Config{Authenticator: stubAuthenticator{identity: testIdentity()}, ControlPlane: &stubControlPlane{err: service.ErrWorkerVersionExists}})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, publishRequest(publishBody(`{}`), "publish-existing-version"))
	if response.Code != http.StatusAccepted {
		t.Fatalf("publish status=%d body=%s", response.Code, response.Body.String())
	}
	location := response.Header().Get("Location")
	deadline := time.Now().Add(time.Second)
	for {
		poll := httptest.NewRecorder()
		handler.ServeHTTP(poll, httptest.NewRequest(http.MethodGet, location, nil))
		if poll.Code == http.StatusOK && strings.Contains(poll.Body.String(), `"state":"failed"`) {
			if !strings.Contains(poll.Body.String(), `"code":"worker_version_exists"`) || !strings.Contains(poll.Body.String(), "publish a new version") {
				t.Fatalf("unclear failed operation: %s", poll.Body.String())
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("operation did not fail: status=%d body=%s", poll.Code, poll.Body.String())
		}
		time.Sleep(time.Millisecond)
	}
}

func TestPublishRequiresIdempotencyKey(t *testing.T) {
	handler := New(Config{Authenticator: stubAuthenticator{identity: testIdentity()}, ControlPlane: &stubControlPlane{}})
	request := publishRequest(publishBody(`{}`), "")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "Idempotency-Key") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func publishRequest(body, key string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/workers/hello-worker/versions", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", "csrf-a")
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	return request
}

func publishBody(versionConfig string) string {
	var config any
	if err := json.Unmarshal([]byte(versionConfig), &config); err != nil {
		panic(err)
	}
	value := map[string]any{
		"version": "v1", "description": "First release",
		"image":         "registry.example.com/hello@sha256:" + strings.Repeat("a", 64),
		"runtime":       map[string]any{"cpu": "100m", "memory": "128Mi", "environment": []any{}},
		"versionConfig": config,
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func TestPublishRejectsUserSuppliedProvenance(t *testing.T) {
	handler := New(Config{Authenticator: stubAuthenticator{identity: testIdentity()}, ControlPlane: &stubControlPlane{}})
	for _, field := range []string{
		`"source":{"repository":"https://example.com/repo","commit":"ccccccc","ciReference":"ci-1"}`,
		`"repository":"https://example.com/repo"`, `"branch":"main"`, `"commit":"ccccccc"`, `"ciReference":"ci-1"`,
	} {
		body := strings.TrimSuffix(publishBody(`{}`), "}") + "," + field + "}"
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, publishRequest(body, "publish-provenance"))
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"validation_failed"`) {
			t.Fatalf("field=%s status=%d body=%s", field, response.Code, response.Body.String())
		}
	}
}

func TestPublishDefaultsOmittedVersionConfigAndDoesNotAcceptSource(t *testing.T) {
	published := make(chan domain.WorkerVersionRequest, 1)
	handler := New(Config{Authenticator: stubAuthenticator{identity: testIdentity()}, ControlPlane: &stubControlPlane{published: published}})
	body := `{"version":"v2","description":"Second release","image":"registry.example.com/hello@sha256:` + strings.Repeat("b", 64) + `","runtime":{"cpu":"100m","memory":"128Mi"}}`
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, publishRequest(body, "publish-v2"))
	if response.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	select {
	case got := <-published:
		if string(got.VersionConfig) != `{}` || got.Source != (domain.SourceProvenance{}) {
			t.Fatalf("publish request config/source = %s / %#v", got.VersionConfig, got.Source)
		}
	case <-time.After(time.Second):
		t.Fatal("publish did not reach control plane")
	}
}

func TestPublishRejectsEditableContractAliasesAndTenantFields(t *testing.T) {
	handler := New(Config{Authenticator: stubAuthenticator{identity: testIdentity()}, ControlPlane: &stubControlPlane{}})
	for _, body := range []string{
		`{"version":"v1","description":"x","metadata":{}}`,
		`{"version":"v1","description":"x","projection":{}}`,
		`{"version":"v1","description":"x","contractArtifact":{}}`,
		`{"version":"v1","description":"x","manifestDigest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`,
		`{"version":"v1","description":"x","tenantId":"tenant-b"}`,
		`{"version":"v1","description":"x","scope":"legacy"}`,
	} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/workers/hello-worker/versions", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-CSRF-Token", "csrf-a")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d response=%s", body, response.Code, response.Body.String())
		}
	}
}

func TestDescriptionPatchRequiresVersionETagAndReturnsNewRevision(t *testing.T) {
	version := consoleVersion(time.Now().UTC())
	backend := &stubControlPlane{version: version, updatedVersion: version}
	backend.updatedVersion.Description = "Revised release note"
	backend.updatedVersion.Revision = 4
	handler := New(Config{Authenticator: stubAuthenticator{identity: testIdentity()}, ControlPlane: backend})

	missing := httptest.NewRequest(http.MethodPatch, "/api/v1/workers/hello-worker/versions/v1/description", strings.NewReader(`{"description":"Revised release note"}`))
	missing.Header.Set("X-CSRF-Token", "csrf-a")
	missingResponse := httptest.NewRecorder()
	handler.ServeHTTP(missingResponse, missing)
	if missingResponse.Code != http.StatusPreconditionRequired {
		t.Fatalf("missing If-Match status=%d body=%s", missingResponse.Code, missingResponse.Body.String())
	}

	request := httptest.NewRequest(http.MethodPatch, "/api/v1/workers/hello-worker/versions/v1/description", strings.NewReader(`{"description":"Revised release note"}`))
	request.Header.Set("X-CSRF-Token", "csrf-a")
	request.Header.Set("If-Match", `"version-v1-r3"`)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("ETag") != `"version-v1-r4"` || !strings.Contains(response.Body.String(), `"description":"Revised release note"`) {
		t.Fatalf("status=%d etag=%q body=%s", response.Code, response.Header().Get("ETag"), response.Body.String())
	}
}
