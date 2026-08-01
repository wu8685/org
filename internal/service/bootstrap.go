package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/wu8685/org/internal/domain"
)

var (
	ErrBootstrapExpired          = errors.New("bootstrap credential expired")
	ErrBootstrapRejected         = errors.New("bootstrap registration rejected")
	ErrBootstrapConflict         = errors.New("bootstrap registration conflicts with accepted contract")
	errBootstrapWorkloadIdentity = errors.New("bootstrap workload identity mismatch")
	errBootstrapImageMismatch    = errors.New("bootstrap workload image mismatch")
)

type BootstrapMaterial struct {
	Token     string
	ExpiresAt time.Time
}

type BootstrapWorkloadEvidence struct {
	PodUID               string `json:"podUid"`
	ObservedImage        string `json:"observedImage"`
	RuntimeImageID       string `json:"runtimeImageId,omitempty"`
	RuntimeLinkVerified  bool   `json:"-"`
	ServiceAccount       string `json:"serviceAccount"`
	AudienceVerified     bool   `json:"-"`
	TenantHash           string `json:"-"`
	WorkerName           string `json:"-"`
	VersionHash          string `json:"-"`
	DeploymentGeneration string `json:"-"`
	OwnerDeployment      string `json:"-"`
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
		return errBootstrapWorkloadIdentity
	}
	if evidence.TenantHash == "" || evidence.TenantHash != binding.TenantHash || evidence.WorkerName != binding.WorkerName || evidence.VersionHash == "" || evidence.VersionHash != binding.VersionHash || evidence.DeploymentGeneration == "" || evidence.DeploymentGeneration != binding.DeploymentGeneration || evidence.OwnerDeployment == "" || evidence.OwnerDeployment != binding.ExpectedDeployment {
		return errBootstrapWorkloadIdentity
	}
	if evidence.ObservedImage != binding.ExpectedImage || (evidence.RuntimeImageID != "" && evidence.RuntimeImageID != binding.ExpectedImage && !evidence.RuntimeLinkVerified) {
		return errBootstrapImageMismatch
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
	if version.ID == "" || version.TenantID == "" || version.TenantSlug == "" || version.TenantHash == "" || version.WorkerName == "" || version.Version == "" || version.VersionHash == "" || version.Image == "" || version.KubernetesServiceAccount == "" || version.KubernetesDeployment == "" || strings.TrimSpace(deploymentGeneration) == "" {
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
	issuedAt := r.cfg.Now().UTC()
	expires := issuedAt.Add(r.cfg.TTL)
	credential := domain.BootstrapCredential{TokenHash: hex.EncodeToString(hash[:]), Binding: domain.BootstrapBinding{
		TenantID: version.TenantID, TenantSlug: version.TenantSlug, TenantHash: version.TenantHash, WorkerName: version.WorkerName,
		WorkerVersionID: version.ID, Version: version.Version, VersionHash: version.VersionHash, ExpectedImage: version.Image, ExpectedServiceAccount: version.KubernetesServiceAccount, ExpectedDeployment: version.KubernetesDeployment,
		DeploymentGeneration: deploymentGeneration, ExpiresAt: expires,
	}}
	audit := bootstrapCredentialIssuedAudit(credential.Binding, issuedAt)
	if err := r.store.CommitBootstrapCredentialAudits(version.TenantID, credential, []domain.AuditRecord{audit}); err != nil {
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
	verificationErr := errBootstrapWorkloadIdentity
	if r.cfg.Verifier != nil {
		verificationErr = r.cfg.Verifier.VerifyBootstrapWorkload(ctx, credential.Binding, evidence)
	}
	verificationFailed := verificationErr != nil
	verificationErrorClass := "workload_identity_mismatch"
	if errors.Is(verificationErr, errBootstrapImageMismatch) {
		verificationErrorClass = "image_mismatch"
	}
	if credential.AcceptedAt != nil && verificationFailed {
		if auditErr := r.auditBootstrapRejection(credential, evidence, request, verificationErrorClass, false, now); auditErr != nil {
			return BootstrapRegistrationReceipt{}, errors.Join(ErrBootstrapRejected, auditErr)
		}
		return BootstrapRegistrationReceipt{}, ErrBootstrapRejected
	}
	if verificationFailed {
		if auditErr := r.auditBootstrapRejection(credential, evidence, request, verificationErrorClass, true, now); auditErr != nil {
			return BootstrapRegistrationReceipt{}, errors.Join(ErrBootstrapRejected, auditErr)
		}
		return BootstrapRegistrationReceipt{}, ErrBootstrapRejected
	}
	if credential.AcceptedAt != nil {
		if credential.RegistrationKey != key {
			if auditErr := r.auditBootstrapRejection(credential, evidence, request, "registration_conflict", false, now); auditErr != nil {
				return BootstrapRegistrationReceipt{}, errors.Join(ErrBootstrapConflict, auditErr)
			}
			return BootstrapRegistrationReceipt{}, ErrBootstrapConflict
		}
		if credential.ReceiptUntil != nil && now.After(*credential.ReceiptUntil) {
			if auditErr := r.auditBootstrapRejection(credential, evidence, request, "receipt_expired", false, now); auditErr != nil {
				return BootstrapRegistrationReceipt{}, errors.Join(ErrBootstrapExpired, auditErr)
			}
			return BootstrapRegistrationReceipt{}, ErrBootstrapExpired
		}
		received := bootstrapAudit(credential.Binding, evidence, request, "worker.bootstrap.registration.received", "exact-retry", "", credential.ReceiptID, now)
		if err := r.store.CommitBootstrapCredentialAudits(credential.Binding.TenantID, credential, []domain.AuditRecord{received}); err != nil {
			return BootstrapRegistrationReceipt{}, err
		}
		return BootstrapRegistrationReceipt{ID: credential.ReceiptID, WorkerVersionID: credential.Binding.WorkerVersionID, ManifestDigest: request.ManifestDigest, AcceptedAt: *credential.AcceptedAt, ExactRetry: true}, nil
	}
	if credential.Revoked {
		if auditErr := r.auditBootstrapRejection(credential, evidence, request, "credential_revoked", false, now); auditErr != nil {
			return BootstrapRegistrationReceipt{}, errors.Join(ErrBootstrapRejected, auditErr)
		}
		return BootstrapRegistrationReceipt{}, ErrBootstrapRejected
	}
	if !now.Before(credential.Binding.ExpiresAt) {
		if auditErr := r.auditBootstrapRejection(credential, evidence, request, "credential_expired", false, now); auditErr != nil {
			return BootstrapRegistrationReceipt{}, errors.Join(ErrBootstrapExpired, auditErr)
		}
		return BootstrapRegistrationReceipt{}, ErrBootstrapExpired
	}
	if err := domain.ValidateWorkerContractRegistration(request, credential.Binding.Version); err != nil {
		if auditErr := r.auditBootstrapRejection(credential, evidence, request, "contract_invalid", true, now); auditErr != nil {
			return BootstrapRegistrationReceipt{}, errors.Join(ErrBootstrapRejected, err, auditErr)
		}
		return BootstrapRegistrationReceipt{}, errors.Join(ErrBootstrapRejected, err)
	}
	version, ok := r.store.WorkerVersion(credential.Binding.TenantID, credential.Binding.WorkerName, credential.Binding.Version)
	if !ok || version.ID != credential.Binding.WorkerVersionID || version.Image != credential.Binding.ExpectedImage || version.State != domain.WorkerVersionPending || version.ManifestDigest != "" {
		if auditErr := r.auditBootstrapRejection(credential, evidence, request, "release_mismatch", false, now); auditErr != nil {
			return BootstrapRegistrationReceipt{}, errors.Join(ErrBootstrapRejected, auditErr)
		}
		return BootstrapRegistrationReceipt{}, ErrBootstrapRejected
	}
	accepted := now.UTC()
	receiptUntil := accepted.Add(r.cfg.ReceiptGrace)
	credential.RegistrationKey, credential.ReceiptID = key, randomID("reg")
	credential.AcceptedAt, credential.ReceiptUntil, credential.Revoked = &accepted, &receiptUntil, true
	version.ManifestDigest, version.Metadata = request.ManifestDigest, request.Metadata
	version.RegistrationStatus, version.RegisteredAt, version.UpdatedAt = domain.BootstrapRegistrationAccepted, &accepted, accepted
	version.PromotionPhase, version.PromotionAttemptID, version.PromotionUpdatedAt = domain.WorkerVersionPromotionQueued, randomID("promotion"), &accepted
	contractAudit := bootstrapAudit(credential.Binding, evidence, request, "worker.contract.register", "accepted", "", credential.ReceiptID, accepted)
	audits := []domain.AuditRecord{
		bootstrapAudit(credential.Binding, evidence, request, "worker.bootstrap.registration.received", "received", "", credential.ReceiptID, accepted),
		bootstrapAudit(credential.Binding, evidence, request, "worker.bootstrap.registration.verified", "accepted", "", credential.ReceiptID, accepted),
		bootstrapAudit(credential.Binding, evidence, request, "worker.bootstrap.credential.revoked", "consumed", "", credential.ReceiptID, accepted),
		contractAudit,
	}
	if err := r.store.CommitBootstrapAcceptance(version.TenantID, version, credential, audits); err != nil {
		return BootstrapRegistrationReceipt{}, err
	}
	return BootstrapRegistrationReceipt{ID: credential.ReceiptID, WorkerVersionID: version.ID, ManifestDigest: request.ManifestDigest, AcceptedAt: accepted}, nil
}

func (r *BootstrapRegistry) auditBootstrapRejection(credential domain.BootstrapCredential, evidence BootstrapWorkloadEvidence, request domain.WorkerContractRegistration, errorClass string, rejectVersion bool, now time.Time) error {
	wasRevoked := credential.Revoked
	credential.Revoked = true
	audits := []domain.AuditRecord{
		bootstrapAudit(credential.Binding, evidence, request, "worker.bootstrap.registration.received", "received", "", credential.ReceiptID, now),
		bootstrapAudit(credential.Binding, evidence, request, "worker.bootstrap.registration.rejected", "rejected", errorClass, credential.ReceiptID, now),
	}
	if !wasRevoked {
		audits = append(audits, bootstrapAudit(credential.Binding, evidence, request, "worker.bootstrap.credential.revoked", "revoked", errorClass, credential.ReceiptID, now))
	}
	if rejectVersion && credential.AcceptedAt == nil && !wasRevoked {
		version, ok := r.store.WorkerVersion(credential.Binding.TenantID, credential.Binding.WorkerName, credential.Binding.Version)
		if ok && version.ID == credential.Binding.WorkerVersionID {
			version.RegistrationStatus = domain.BootstrapRegistrationRejected
			return r.store.CommitBootstrapRejection(credential.Binding.TenantID, version, credential, audits)
		}
	}
	return r.store.CommitBootstrapCredentialAudits(credential.Binding.TenantID, credential, audits)
}

func validateBootstrapAcceptance(tenantID string, currentVersion, acceptedVersion domain.WorkerVersion, currentCredential, acceptedCredential domain.BootstrapCredential, audits []domain.AuditRecord) error {
	if tenantID == "" || acceptedVersion.TenantID != tenantID {
		return errors.New("bootstrap acceptance tenant identity mismatch")
	}
	if currentVersion.ID == "" || currentVersion.ID != acceptedVersion.ID || currentVersion.WorkerName != acceptedVersion.WorkerName || currentVersion.Version != acceptedVersion.Version || currentVersion.Image != acceptedVersion.Image {
		return ErrBootstrapRejected
	}
	if currentVersion.State != domain.WorkerVersionPending || currentVersion.ManifestDigest != "" || currentVersion.RegistrationStatus == domain.BootstrapRegistrationAccepted {
		return ErrBootstrapConflict
	}
	if acceptedVersion.State != domain.WorkerVersionPending || acceptedVersion.ManifestDigest == "" || acceptedVersion.RegistrationStatus != domain.BootstrapRegistrationAccepted || acceptedVersion.RegisteredAt == nil {
		return ErrBootstrapRejected
	}
	expectedVersion := currentVersion
	expectedVersion.ManifestDigest = acceptedVersion.ManifestDigest
	expectedVersion.Metadata = acceptedVersion.Metadata
	expectedVersion.RegistrationStatus = acceptedVersion.RegistrationStatus
	expectedVersion.RegisteredAt = acceptedVersion.RegisteredAt
	expectedVersion.UpdatedAt = acceptedVersion.UpdatedAt
	expectedVersion.PromotionPhase = acceptedVersion.PromotionPhase
	expectedVersion.PromotionAttemptID = acceptedVersion.PromotionAttemptID
	expectedVersion.PromotionUpdatedAt = acceptedVersion.PromotionUpdatedAt
	if !reflect.DeepEqual(expectedVersion, acceptedVersion) {
		return ErrBootstrapRejected
	}
	if acceptedVersion.PromotionPhase != domain.WorkerVersionPromotionQueued || acceptedVersion.PromotionAttemptID == "" || acceptedVersion.PromotionUpdatedAt == nil {
		return ErrBootstrapRejected
	}
	if currentCredential.TokenHash == "" || currentCredential.TokenHash != acceptedCredential.TokenHash || currentCredential.Binding != acceptedCredential.Binding || currentCredential.AcceptedAt != nil || currentCredential.Revoked {
		return ErrBootstrapConflict
	}
	if acceptedCredential.AcceptedAt == nil || acceptedCredential.ReceiptUntil == nil || acceptedCredential.ReceiptID == "" || acceptedCredential.RegistrationKey == "" || !acceptedCredential.Revoked {
		return ErrBootstrapRejected
	}
	expectedCredential := currentCredential
	expectedCredential.RegistrationKey = acceptedCredential.RegistrationKey
	expectedCredential.ReceiptID = acceptedCredential.ReceiptID
	expectedCredential.AcceptedAt = acceptedCredential.AcceptedAt
	expectedCredential.ReceiptUntil = acceptedCredential.ReceiptUntil
	expectedCredential.Revoked = true
	if !reflect.DeepEqual(expectedCredential, acceptedCredential) {
		return ErrBootstrapRejected
	}
	var acceptedAudit domain.AuditRecord
	for _, audit := range audits {
		if audit.Action == "worker.contract.register" {
			acceptedAudit = audit
		}
	}
	if acceptedAudit.ID == "" || acceptedAudit.TenantID != tenantID || acceptedAudit.TenantSlug != acceptedVersion.TenantSlug || acceptedAudit.PrincipalID != "worker-bootstrap" || acceptedAudit.AuthenticationMethod != "kubernetes-tokenreview" || acceptedAudit.Permission != "bootstrap:register-contract" || acceptedAudit.AuthorizationResult != "allowed" || acceptedAudit.TargetType != "workerVersion" || acceptedAudit.TargetID != acceptedVersion.ID || acceptedAudit.Outcome != "accepted" {
		return errors.New("accepted bootstrap audit is required")
	}
	if err := validateBootstrapAudits(tenantID, acceptedVersion.TenantSlug, acceptedVersion.ID, audits); err != nil {
		return err
	}
	return nil
}

func (s *MemoryStore) CommitBootstrapAcceptance(tenantID string, version domain.WorkerVersion, credential domain.BootstrapCredential, audits []domain.AuditRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	versionKey := tenantKey(tenantID, version.ID)
	currentVersion, versionExists := s.versions[versionKey]
	currentCredential, credentialExists := s.bootstrapCredentials[credential.TokenHash]
	if !versionExists || !credentialExists {
		return ErrBootstrapRejected
	}
	if err := validateBootstrapAcceptance(tenantID, currentVersion, version, currentCredential, credential, audits); err != nil {
		return err
	}
	s.versions[versionKey] = version
	s.bootstrapCredentials[credential.TokenHash] = credential
	s.audits[tenantID] = append(s.audits[tenantID], audits...)
	return nil
}

func (s *FileStore) CommitBootstrapAcceptance(tenantID string, version domain.WorkerVersion, credential domain.BootstrapCredential, audits []domain.AuditRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mutate(func(next *fileState) error {
		versionKey := tenantKey(tenantID, version.ID)
		currentVersion, versionExists := next.WorkerVersions[versionKey]
		currentCredential, credentialExists := next.BootstrapCredentials[credential.TokenHash]
		if !versionExists || !credentialExists {
			return ErrBootstrapRejected
		}
		if err := validateBootstrapAcceptance(tenantID, currentVersion, version, currentCredential, credential, audits); err != nil {
			return err
		}
		next.WorkerVersions[versionKey] = version
		next.WorkerVersionRouting[versionKey] = captureWorkerVersionRouting(version)
		next.BootstrapCredentials[credential.TokenHash] = credential
		next.Audits[tenantID] = append(next.Audits[tenantID], audits...)
		return nil
	})
}

func bootstrapAudit(binding domain.BootstrapBinding, evidence BootstrapWorkloadEvidence, request domain.WorkerContractRegistration, action, outcome, errorClass, receiptID string, now time.Time) domain.AuditRecord {
	references := map[string]string{"workerName": binding.WorkerName, "version": binding.Version, "imageDigest": binding.ExpectedImage}
	if request.ManifestDigest != "" {
		references["manifestDigest"] = request.ManifestDigest
	}
	if receiptID != "" {
		references["receiptId"] = receiptID
	}
	if evidence.PodUID != "" {
		digest := sha256.Sum256([]byte(evidence.PodUID))
		references["podUidHash"] = hex.EncodeToString(digest[:])
	}
	return domain.AuditRecord{
		ID: randomID("aud"), TenantID: binding.TenantID, TenantSlug: binding.TenantSlug,
		PrincipalID: "worker-bootstrap", AuthenticationMethod: "kubernetes-tokenreview", RequestID: receiptID,
		Action: action, Permission: "bootstrap:register-contract", AuthorizationResult: "allowed", Outcome: outcome,
		TargetType: "workerVersion", TargetID: binding.WorkerVersionID, ErrorClass: errorClass, References: references, CreatedAt: now.UTC(),
	}
}

func bootstrapCredentialIssuedAudit(binding domain.BootstrapBinding, now time.Time) domain.AuditRecord {
	return domain.AuditRecord{
		ID: randomID("aud"), TenantID: binding.TenantID, TenantSlug: binding.TenantSlug,
		PrincipalID: "bootstrap-controller", AuthenticationMethod: "internal-controller",
		Action: "worker.bootstrap.credential.issued", Permission: "bootstrap:issue", AuthorizationResult: "allowed", Outcome: "success",
		TargetType: "workerVersion", TargetID: binding.WorkerVersionID,
		References: map[string]string{"workerName": binding.WorkerName, "version": binding.Version, "imageDigest": binding.ExpectedImage}, CreatedAt: now.UTC(),
	}
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
