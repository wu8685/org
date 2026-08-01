package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/wu8685/org/internal/domain"
)

type BootstrapRegistrationService interface {
	RegisterBootstrap(context.Context, string, BootstrapWorkloadEvidence, domain.WorkerContractRegistration) (BootstrapRegistrationReceipt, domain.WorkerVersion, error)
}
type BootstrapPromotionScheduler interface {
	ScheduleBootstrapPromotion(context.Context, BootstrapRegistrationReceipt) error
}

type BootstrapEvidenceResolver interface {
	ResolveBootstrapEvidence(*http.Request) (BootstrapWorkloadEvidence, error)
}
type BootstrapEvidenceResolverFunc func(*http.Request) (BootstrapWorkloadEvidence, error)

func (f BootstrapEvidenceResolverFunc) ResolveBootstrapEvidence(request *http.Request) (BootstrapWorkloadEvidence, error) {
	return f(request)
}

func NewBootstrapRegistrationHandler(service BootstrapRegistrationService, resolver BootstrapEvidenceResolver) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		token := strings.TrimSpace(strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer "))
		if token == "" || resolver == nil {
			writeBootstrapRejected(response, http.StatusUnauthorized, "authentication-required")
			return
		}
		evidence, err := resolver.ResolveBootstrapEvidence(request)
		if err != nil {
			writeBootstrapRejected(response, http.StatusUnauthorized, "workload-identity")
			return
		}
		var input struct {
			ManifestDigest string                `json:"manifestDigest"`
			Contract       domain.WorkerMetadata `json:"contract"`
			BuildID        string                `json:"buildId"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			writeBootstrapRejected(response, http.StatusBadRequest, "invalid-contract")
			return
		}
		receipt, _, err := service.RegisterBootstrap(request.Context(), token, evidence, domain.WorkerContractRegistration{ManifestDigest: input.ManifestDigest, Metadata: input.Contract, BuildID: input.BuildID})
		if err != nil {
			status := http.StatusForbidden
			if errors.Is(err, ErrBootstrapConflict) {
				status = http.StatusConflict
			}
			writeBootstrapRejected(response, status, "registration-rejected")
			return
		}
		scheduler, ok := service.(BootstrapPromotionScheduler)
		if !ok || scheduler.ScheduleBootstrapPromotion(request.Context(), receipt) != nil {
			response.Header().Set("Content-Type", "application/json")
			response.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(response).Encode(map[string]any{"state": "accepted", "reason": "promotion-pending", "receiptId": receipt.ID})
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(map[string]any{"state": "accepted", "receiptId": receipt.ID})
	})
}

func writeBootstrapRejected(response http.ResponseWriter, status int, reason string) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(map[string]string{"state": "rejected", "reason": reason})
}
