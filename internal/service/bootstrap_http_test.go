package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wu8685/org/internal/domain"
)

func TestBootstrapHTTPDerivesTargetFromBearerAndReturnsAcceptedReceipt(t *testing.T) {
	contract := bootstrapContract(t, "v1")
	body, _ := json.Marshal(map[string]any{"manifestDigest": contract.ManifestDigest, "contract": contract.Metadata, "buildId": contract.BuildID})
	backend := &fakeBootstrapService{receipt: BootstrapRegistrationReceipt{ID: "reg-1", WorkerVersionID: "ver-1"}}
	handler := NewBootstrapRegistrationHandler(backend, BootstrapEvidenceResolverFunc(func(*http.Request) (BootstrapWorkloadEvidence, error) {
		return BootstrapWorkloadEvidence{ObservedImage: "registry.example.com/acme/worker@sha256:" + strings.Repeat("a", 64), AudienceVerified: true}, nil
	}))
	request := httptest.NewRequest(http.MethodPost, "/internal/v1/bootstrap/register", strings.NewReader(string(body)))
	request.Header.Set("Authorization", "Bearer opaque-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"state":"accepted"`) || backend.token != "opaque-token" {
		t.Fatalf("status=%d body=%s token=%q", response.Code, response.Body.String(), backend.token)
	}
}

func TestBootstrapHTTPRejectionsUseMachineReadableStateWithoutCredentialEcho(t *testing.T) {
	handler := NewBootstrapRegistrationHandler(&fakeBootstrapService{}, nil)
	request := httptest.NewRequest(http.MethodPost, "/internal/v1/bootstrap/register", strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer do-not-echo")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), `"state":"rejected"`) || strings.Contains(response.Body.String(), "do-not-echo") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

type fakeBootstrapService struct {
	token   string
	receipt BootstrapRegistrationReceipt
}

func (f *fakeBootstrapService) RegisterBootstrap(_ context.Context, token string, _ BootstrapWorkloadEvidence, _ domain.WorkerContractRegistration) (BootstrapRegistrationReceipt, domain.WorkerVersion, error) {
	f.token = token
	return f.receipt, domain.WorkerVersion{}, nil
}
