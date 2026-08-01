package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/wu8685/org/internal/domain"
)

var (
	ErrBootstrapExpired  = errors.New("bootstrap credential expired")
	ErrBootstrapRejected = errors.New("bootstrap registration rejected")
	ErrBootstrapConflict = errors.New("bootstrap registration conflicts with accepted contract")
)

type BootstrapMaterial struct {
	Token     string
	ExpiresAt time.Time
}

type BootstrapWorkloadEvidence struct {
	PodUID              string `json:"podUid"`
	ObservedImage       string `json:"observedImage"`
	RuntimeImageID      string `json:"runtimeImageId,omitempty"`
	RuntimeLinkVerified bool   `json:"-"`
	ServiceAccount      string `json:"serviceAccount"`
	AudienceVerified    bool   `json:"-"`
}

type BootstrapWorkloadVerifier interface {
	VerifyBootstrapWorkload(context.Context, domain.BootstrapBinding, BootstrapWorkloadEvidence) error
}

type BootstrapWorkloadVerifierFunc func(context.Context, domain.BootstrapBinding, BootstrapWorkloadEvidence) error

func (f BootstrapWorkloadVerifierFunc) VerifyBootstrapWorkload(ctx context.Context, binding domain.BootstrapBinding, evidence BootstrapWorkloadEvidence) error {
	return f(ctx, binding, evidence)
}

type StrictBootstrapWorkloadVerifier struct{}

func (StrictBootstrapWorkloadVerifier) VerifyBootstrapWorkload(_ context.Context, binding domain.BootstrapBinding, evidence BootstrapWorkloadEvidence) error {
	if !evidence.AudienceVerified || strings.TrimSpace(evidence.PodUID) == "" || evidence.ServiceAccount != binding.ExpectedServiceAccount {
		return errors.New("bound Kubernetes workload identity is required")
	}
	if evidence.ObservedImage != binding.ExpectedImage || (evidence.RuntimeImageID != "" && evidence.RuntimeImageID != binding.ExpectedImage && !evidence.RuntimeLinkVerified) {
		return errors.New("runtime Pod imageID does not match the expected immutable image digest")
	}
	return nil
}

type BootstrapRegistryConfig struct {
	TTL          time.Duration
	ReceiptGrace time.Duration
	Now          func() time.Time
	Verifier     BootstrapWorkloadVerifier
}

type BootstrapRegistrationReceipt struct {
	ID              string    `json:"id"`
	WorkerVersionID string    `json:"workerVersionId"`
	ManifestDigest  string    `json:"manifestDigest"`
	AcceptedAt      time.Time `json:"acceptedAt"`
	ExactRetry      bool      `json:"exactRetry"`
}

type BootstrapRegistry struct {
	store Store
	cfg   BootstrapRegistryConfig
	mu    sync.Mutex
}

func NewBootstrapRegistry(store Store, cfg BootstrapRegistryConfig) *BootstrapRegistry {
	if cfg.TTL <= 0 {
		cfg.TTL = 15 * time.Minute
	}
	if cfg.ReceiptGrace <= 0 {
		cfg.ReceiptGrace = 5 * time.Minute
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	return &BootstrapRegistry{store: store, cfg: cfg}
}

func (r *BootstrapRegistry) Issue(version domain.WorkerVersion, deploymentGeneration string) (BootstrapMaterial, error) {
	if version.ID == "" || version.TenantID == "" || version.WorkerName == "" || version.Version == "" || deploymentGeneration == "" {
		return BootstrapMaterial{}, errors.New("complete pending WorkerVersion binding is required")
	}
	if version.State != domain.WorkerVersionPending || version.ManifestDigest != "" || len(version.Metadata.Workflows) != 0 {
		return BootstrapMaterial{}, errors.New("bootstrap credential requires an unregistered pending WorkerVersion")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return BootstrapMaterial{}, err
	}
	token := hex.EncodeToString(raw)
	hash := sha256.Sum256([]byte(token))
	expires := r.cfg.Now().Add(r.cfg.TTL)
	credential := domain.BootstrapCredential{TokenHash: hex.EncodeToString(hash[:]), Binding: domain.BootstrapBinding{
		TenantID: version.TenantID, TenantSlug: version.TenantSlug, WorkerName: version.WorkerName,
		WorkerVersionID: version.ID, Version: version.Version, ExpectedImage: version.Image, ExpectedServiceAccount: version.KubernetesServiceAccount,
		DeploymentGeneration: deploymentGeneration, ExpiresAt: expires,
	}}
	if err := r.store.SaveBootstrapCredential(credential); err != nil {
		return BootstrapMaterial{}, err
	}
	return BootstrapMaterial{Token: token, ExpiresAt: expires}, nil
}

func (r *BootstrapRegistry) Register(ctx context.Context, token string, evidence BootstrapWorkloadEvidence, request domain.WorkerContractRegistration) (BootstrapRegistrationReceipt, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	hash := sha256.Sum256([]byte(token))
	credential, ok := r.store.BootstrapCredential(hex.EncodeToString(hash[:]))
	if !ok {
		return BootstrapRegistrationReceipt{}, ErrBootstrapRejected
	}
	now := r.cfg.Now()
	key := bootstrapRegistrationKey(credential.Binding, request)
	verificationFailed := r.cfg.Verifier == nil || r.cfg.Verifier.VerifyBootstrapWorkload(ctx, credential.Binding, evidence) != nil
	if credential.AcceptedAt != nil && verificationFailed {
		return BootstrapRegistrationReceipt{}, ErrBootstrapRejected
	}
	if verificationFailed {
		credential.Revoked = true
		_ = r.store.SaveBootstrapCredential(credential)
		r.rejectVersion(credential.Binding)
		return BootstrapRegistrationReceipt{}, ErrBootstrapRejected
	}
	if credential.AcceptedAt != nil {
		if credential.RegistrationKey != key {
			return BootstrapRegistrationReceipt{}, ErrBootstrapConflict
		}
		if credential.ReceiptUntil != nil && now.After(*credential.ReceiptUntil) {
			return BootstrapRegistrationReceipt{}, ErrBootstrapExpired
		}
		return BootstrapRegistrationReceipt{ID: credential.ReceiptID, WorkerVersionID: credential.Binding.WorkerVersionID, ManifestDigest: request.ManifestDigest, AcceptedAt: *credential.AcceptedAt, ExactRetry: true}, nil
	}
	if credential.Revoked {
		return BootstrapRegistrationReceipt{}, ErrBootstrapRejected
	}
	if !now.Before(credential.Binding.ExpiresAt) {
		credential.Revoked = true
		_ = r.store.SaveBootstrapCredential(credential)
		return BootstrapRegistrationReceipt{}, ErrBootstrapExpired
	}
	if err := domain.ValidateWorkerContractRegistration(request, credential.Binding.Version); err != nil {
		credential.Revoked = true
		_ = r.store.SaveBootstrapCredential(credential)
		r.rejectVersion(credential.Binding)
		return BootstrapRegistrationReceipt{}, errors.Join(ErrBootstrapRejected, err)
	}
	version, ok := r.store.WorkerVersion(credential.Binding.TenantID, credential.Binding.WorkerName, credential.Binding.Version)
	if !ok || version.ID != credential.Binding.WorkerVersionID || version.Image != credential.Binding.ExpectedImage || version.State != domain.WorkerVersionPending || version.ManifestDigest != "" {
		return BootstrapRegistrationReceipt{}, ErrBootstrapRejected
	}
	accepted := now.UTC()
	receiptUntil := accepted.Add(r.cfg.ReceiptGrace)
	credential.RegistrationKey, credential.ReceiptID = key, randomID("reg")
	credential.AcceptedAt, credential.ReceiptUntil, credential.Revoked = &accepted, &receiptUntil, true
	version.ManifestDigest, version.Metadata = request.ManifestDigest, request.Metadata
	version.RegistrationStatus, version.RegisteredAt, version.UpdatedAt = domain.BootstrapRegistrationAccepted, &accepted, accepted
	if err := r.store.SaveWorkerVersion(version.TenantID, version); err != nil {
		return BootstrapRegistrationReceipt{}, err
	}
	if err := r.store.SaveBootstrapCredential(credential); err != nil {
		return BootstrapRegistrationReceipt{}, err
	}
	if err := r.store.AppendAudit(version.TenantID, domain.AuditRecord{
		ID: randomID("aud"), TenantID: version.TenantID, TenantSlug: version.TenantSlug,
		PrincipalID: "worker-bootstrap", AuthenticationMethod: "kubernetes-tokenreview",
		Action: "worker.contract.register", Permission: "bootstrap:register-contract", AuthorizationResult: "allowed", Outcome: "accepted",
		TargetType: "workerVersion", TargetID: version.ID, References: map[string]string{"workerName": version.WorkerName, "version": version.Version, "manifestDigest": request.ManifestDigest}, CreatedAt: accepted,
	}); err != nil {
		return BootstrapRegistrationReceipt{}, err
	}
	return BootstrapRegistrationReceipt{ID: credential.ReceiptID, WorkerVersionID: version.ID, ManifestDigest: request.ManifestDigest, AcceptedAt: accepted}, nil
}

func (r *BootstrapRegistry) rejectVersion(binding domain.BootstrapBinding) {
	version, ok := r.store.WorkerVersion(binding.TenantID, binding.WorkerName, binding.Version)
	if !ok || version.ID != binding.WorkerVersionID {
		return
	}
	version.RegistrationStatus = domain.BootstrapRegistrationRejected
	_ = r.store.SaveWorkerVersion(binding.TenantID, version)
}

func bootstrapRegistrationKey(binding domain.BootstrapBinding, request domain.WorkerContractRegistration) string {
	material, _ := json.Marshal(struct {
		WorkerVersionID, ExpectedImage, ManifestDigest, SDKModuleVersion, RuntimeProtocolVersion, ContractVersion, BuildID string
	}{binding.WorkerVersionID, binding.ExpectedImage, request.ManifestDigest, request.Metadata.SDK.ModuleVersion, request.Metadata.SDK.RuntimeProtocolVersion, request.Metadata.ContractVersion, request.BuildID})
	sum := sha256.Sum256(material)
	return hex.EncodeToString(sum[:])
}

func randomID(prefix string) string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return strings.TrimSpace(prefix) + "-" + hex.EncodeToString(b)
}
