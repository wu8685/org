package console

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wu8685/org/internal/domain"
)

func TestPublishWorkerVersionReturns202AndPollableOperation(t *testing.T) {
	published := make(chan domain.WorkerVersionRequest, 1)
	backend := &stubControlPlane{published: published, version: consoleVersion(time.Now().UTC())}
	handler := New(Config{Authenticator: stubAuthenticator{identity: testIdentity()}, ControlPlane: backend})
	body := `{
      "version":"v1",
      "description":"First release",
      "image":"registry.example.com/hello@sha256:` + strings.Repeat("a", 64) + `",
      "versionConfig":{"region":"local","provider":{"secretRef":"provider-token"}},
      "runtime":{"cpu":"100m","memory":"128Mi","environment":[]},
      "source":{"repository":"https://example.com/repo","branch":"main","commit":"cccccccccccc","ciReference":"ci-1"}
    }`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/workers/hello-worker/versions", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", "csrf-a")
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
