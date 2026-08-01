package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/wu8685/org/internal/domain"
)

type BootstrapRegistrationService interface {
	RegisterBootstrap(context.Context, string, BootstrapWorkloadEvidence, domain.WorkerContractRegistration) (BootstrapRegistrationReceipt, domain.WorkerVersion, error)
}
type BootstrapPromotionService interface {
	PromoteBootstrap(context.Context, BootstrapRegistrationReceipt) (domain.WorkerVersion, error)
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
		if promoter, ok := service.(BootstrapPromotionService); ok {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
				defer cancel()
				_, _ = promoter.PromoteBootstrap(ctx, receipt)
			}()
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
