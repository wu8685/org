package console

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wu8685/org/internal/domain"
	"github.com/wu8685/org/internal/service"
	"github.com/wu8685/org/sdk/orgsdk"
)

func TestStartRunUsesRouteIdentityAndHeaderIdempotencyKey(t *testing.T) {
	backend := &stubControlPlane{startedRun: domain.Invocation{ID: "inv-1", WorkerName: "decision-worker", Workflow: "DecisionWorkflow", SelectedVersion: "v1", Description: "Why now\n<script>alert(1)</script>"}}
	handler := New(Config{Authenticator: stubAuthenticator{identity: testIdentity()}, ControlPlane: backend})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/workers/decision-worker/workflows/DecisionWorkflow/runs", strings.NewReader(`{"workerVersion":"v1","description":"Why now\n<script>alert(1)</script>","input":{"nested":{"routes":["short",{"fallback":true}]}}}`))
	request.Header.Set("X-CSRF-Token", "csrf-a")
	request.Header.Set("Idempotency-Key", "start-42")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), `"selectedVersion":"v1"`) || !strings.Contains(response.Body.String(), `"description":"Why now\n\u003cscript\u003ealert(1)\u003c/script\u003e"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if backend.startRequest.WorkerName != "decision-worker" || backend.startRequest.Workflow != "DecisionWorkflow" || backend.startRequest.Description != "Why now\n<script>alert(1)</script>" || backend.startRequest.IdempotencyKey != "start-42" || !strings.Contains(string(backend.startRequest.Input), `"routes":["short",{"fallback":true}]`) {
		t.Fatalf("start request=%#v", backend.startRequest)
	}
}

func TestStartRunRejectsForgedIdentityFields(t *testing.T) {
	handler := New(Config{Authenticator: stubAuthenticator{identity: testIdentity()}, ControlPlane: &stubControlPlane{}})
	for _, body := range []string{
		`{"input":{},"tenantId":"tenant-b"}`,
		`{"input":{},"scope":"legacy"}`,
		`{"input":{},"workerName":"forged"}`,
		`{"input":{},"workflow":"forged"}`,
	} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/workers/decision-worker/workflows/DecisionWorkflow/runs", strings.NewReader(body))
		request.Header.Set("X-CSRF-Token", "csrf-a")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d response=%s", body, response.Code, response.Body.String())
		}
	}
}

func TestCancelRunIsAcceptedWithoutClaimingExternalEffectsWereReversed(t *testing.T) {
	backend := &stubControlPlane{}
	handler := New(Config{Authenticator: stubAuthenticator{identity: testIdentity()}, ControlPlane: backend})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/runs/inv-1/cancel", nil)
	request.Header.Set("X-CSRF-Token", "csrf-a")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusAccepted || backend.cancelledRun != "inv-1" || strings.Contains(strings.ToLower(response.Body.String()), "reversed") {
		t.Fatalf("status=%d cancelled=%q body=%s", response.Code, backend.cancelledRun, response.Body.String())
	}
}

func TestActionRequiresOperationKeyAndCurrentProjectionETag(t *testing.T) {
	backend := actionStub()
	backend.actions = nil
	handler := New(Config{Authenticator: stubAuthenticator{identity: testIdentity()}, ControlPlane: backend})
	path := "/api/v1/runs/inv-1/nodes/approval-1/actions/confirm"

	missingKey := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"input":{}}`))
	missingKey.Header.Set("X-CSRF-Token", "csrf-a")
	missingKey.Header.Set("If-Match", `"projection-r7"`)
	missingKeyResponse := httptest.NewRecorder()
	handler.ServeHTTP(missingKeyResponse, missingKey)
	if missingKeyResponse.Code != http.StatusBadRequest {
		t.Fatalf("missing key status=%d body=%s", missingKeyResponse.Code, missingKeyResponse.Body.String())
	}

	missingETag := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"input":{}}`))
	missingETag.Header.Set("X-CSRF-Token", "csrf-a")
	missingETag.Header.Set("Idempotency-Key", "op-1")
	missingETagResponse := httptest.NewRecorder()
	handler.ServeHTTP(missingETagResponse, missingETag)
	if missingETagResponse.Code != http.StatusPreconditionRequired {
		t.Fatalf("missing etag status=%d body=%s", missingETagResponse.Code, missingETagResponse.Body.String())
	}

	stale := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"input":{}}`))
	stale.Header.Set("X-CSRF-Token", "csrf-a")
	stale.Header.Set("Idempotency-Key", "op-1")
	stale.Header.Set("If-Match", `"projection-r6"`)
	staleResponse := httptest.NewRecorder()
	handler.ServeHTTP(staleResponse, stale)
	if staleResponse.Code != http.StatusPreconditionFailed || backend.actionRequest.OperationID != "" {
		t.Fatalf("stale status=%d action=%#v body=%s", staleResponse.Code, backend.actionRequest, staleResponse.Body.String())
	}
}

func TestActionRetryWithSameOperationKeySurvivesProjectionAdvance(t *testing.T) {
	backend := actionStub()
	backend.invocationView.SemanticProjection.Revision = 8
	handler := New(Config{Authenticator: stubAuthenticator{identity: testIdentity()}, ControlPlane: backend})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/runs/inv-1/nodes/approval-1/actions/confirm", strings.NewReader(`{"input":{}}`))
	request.Header.Set("X-CSRF-Token", "csrf-a")
	request.Header.Set("Idempotency-Key", "op-1")
	request.Header.Set("If-Match", `"projection-r7"`)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusAccepted || backend.actionRequest.OperationID != "op-1" || !strings.Contains(response.Body.String(), `"operationId":"op-1"`) {
		t.Fatalf("status=%d action=%#v body=%s", response.Code, backend.actionRequest, response.Body.String())
	}
}

func TestActionReturnsPollableDeliveryStateAndPreservesUnknownOutcome(t *testing.T) {
	for _, test := range []struct {
		name  string
		state domain.ActionDeliveryState
		err   error
	}{
		{name: "delivered", state: domain.ActionDeliveryDelivered},
		{name: "delivery unknown", state: domain.ActionDeliveryUnknown, err: errors.New("signal result unknown")},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := actionStub()
			backend.actionResult.State, backend.actionErr = test.state, test.err
			handler := New(Config{Authenticator: stubAuthenticator{identity: testIdentity()}, ControlPlane: backend})
			request := httptest.NewRequest(http.MethodPost, "/api/v1/runs/inv-1/nodes/approval-1/actions/confirm", strings.NewReader(`{"input":{"approved":true}}`))
			request.Header.Set("X-CSRF-Token", "csrf-a")
			request.Header.Set("Idempotency-Key", "op-1")
			request.Header.Set("If-Match", `"projection-r7"`)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			if response.Code != http.StatusAccepted || !strings.Contains(response.Body.String(), `"state":"`+string(test.state)+`"`) || !strings.Contains(response.Body.String(), `"retrySafe":true`) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if backend.actionRequest.RunID != "inv-1" || backend.actionRequest.RuntimeNodeID != "approval-1" || backend.actionRequest.Action != "confirm" || backend.actionRequest.OperationID != "op-1" {
				t.Fatalf("action request=%#v", backend.actionRequest)
			}

			poll := httptest.NewRecorder()
			handler.ServeHTTP(poll, httptest.NewRequest(http.MethodGet, "/api/v1/runs/inv-1/actions/op-1", nil))
			if poll.Code != http.StatusOK || !strings.Contains(poll.Body.String(), `"operationId":"op-1"`) {
				t.Fatalf("poll status=%d body=%s", poll.Code, poll.Body.String())
			}
		})
	}
}

func actionStub() *stubControlPlane {
	projection := &orgsdk.Projection{Revision: 7}
	operation := domain.ActionOperation{ID: "act-1", RunID: "inv-1", RuntimeNodeID: "approval-1", Action: "confirm", OperationID: "op-1", State: domain.ActionDeliveryDelivered}
	return &stubControlPlane{
		invocationView: service.InvocationView{Invocation: domain.Invocation{ID: "inv-1"}, SemanticProjection: projection},
		actionResult:   operation,
		actions:        []domain.ActionOperation{operation},
	}
}
