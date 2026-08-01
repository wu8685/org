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
	ErrBootstrapExpired  = errors.New("bootstrap credential expired")
	ErrBootstrapRejected = errors.New("bootstrap registration rejected")
	ErrBootstrapConflict = errors.New("bootstrap registration conflicts with accepted contract")
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
		return errors.New("bound Kubernetes workload identity is required")
	}
	if evidence.TenantHash == "" || evidence.TenantHash != binding.TenantHash || evidence.WorkerName != binding.WorkerName || evidence.VersionHash == "" || evidence.VersionHash != binding.VersionHash || evidence.DeploymentGeneration == "" || evidence.DeploymentGeneration != binding.DeploymentGeneration || evidence.OwnerDeployment == "" || evidence.OwnerDeployment != binding.ExpectedDeployment {
		return errors.New("candidate Pod labels, rollout generation, or Deployment owner do not match the bootstrap binding")
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
	expires := r.cfg.Now().Add(r.cfg.TTL)
	credential := domain.BootstrapCredential{TokenHash: hex.EncodeToString(hash[:]), Binding: domain.BootstrapBinding{
		TenantID: version.TenantID, TenantSlug: version.TenantSlug, TenantHash: version.TenantHash, WorkerName: version.WorkerName,
		WorkerVersionID: version.ID, Version: version.Version, VersionHash: version.VersionHash, ExpectedImage: version.Image, ExpectedServiceAccount: version.KubernetesServiceAccount, ExpectedDeployment: version.KubernetesDeployment,
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
	version.PromotionPhase, version.PromotionAttemptID, version.PromotionUpdatedAt = domain.WorkerVersionPromotionQueued, randomID("promotion"), &accepted
	audit := domain.AuditRecord{
		ID: randomID("aud"), TenantID: version.TenantID, TenantSlug: version.TenantSlug,
		PrincipalID: "worker-bootstrap", AuthenticationMethod: "kubernetes-tokenreview",
		Action: "worker.contract.register", Permission: "bootstrap:register-contract", AuthorizationResult: "allowed", Outcome: "accepted",
		TargetType: "workerVersion", TargetID: version.ID, References: map[string]string{"workerName": version.WorkerName, "version": version.Version, "manifestDigest": request.ManifestDigest}, CreatedAt: accepted,
	}
	if err := r.store.CommitBootstrapAcceptance(version.TenantID, version, credential, audit); err != nil {
		return BootstrapRegistrationReceipt{}, err
	}
	return BootstrapRegistrationReceipt{ID: credential.ReceiptID, WorkerVersionID: version.ID, ManifestDigest: request.ManifestDigest, AcceptedAt: accepted}, nil
}

func validateBootstrapAcceptance(tenantID string, currentVersion, acceptedVersion domain.WorkerVersion, currentCredential, acceptedCredential domain.BootstrapCredential, audit domain.AuditRecord) error {
	if tenantID == "" || acceptedVersion.TenantID != tenantID || audit.TenantID != tenantID {
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
	if audit.ID == "" || audit.TenantSlug != acceptedVersion.TenantSlug || audit.PrincipalID != "worker-bootstrap" || audit.AuthenticationMethod != "kubernetes-tokenreview" || audit.Action != "worker.contract.register" || audit.Permission != "bootstrap:register-contract" || audit.AuthorizationResult != "allowed" || audit.TargetType != "workerVersion" || audit.TargetID != acceptedVersion.ID || audit.Outcome != "accepted" {
		return errors.New("accepted bootstrap audit is required")
	}
	return nil
}

func (s *MemoryStore) CommitBootstrapAcceptance(tenantID string, version domain.WorkerVersion, credential domain.BootstrapCredential, audit domain.AuditRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	versionKey := tenantKey(tenantID, version.ID)
	currentVersion, versionExists := s.versions[versionKey]
	currentCredential, credentialExists := s.bootstrapCredentials[credential.TokenHash]
	if !versionExists || !credentialExists {
		return ErrBootstrapRejected
	}
	if err := validateBootstrapAcceptance(tenantID, currentVersion, version, currentCredential, credential, audit); err != nil {
		return err
	}
	s.versions[versionKey] = version
	s.bootstrapCredentials[credential.TokenHash] = credential
	s.audits[tenantID] = append(s.audits[tenantID], audit)
	return nil
}

func (s *FileStore) CommitBootstrapAcceptance(tenantID string, version domain.WorkerVersion, credential domain.BootstrapCredential, audit domain.AuditRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mutate(func(next *fileState) error {
		versionKey := tenantKey(tenantID, version.ID)
		currentVersion, versionExists := next.WorkerVersions[versionKey]
		currentCredential, credentialExists := next.BootstrapCredentials[credential.TokenHash]
		if !versionExists || !credentialExists {
			return ErrBootstrapRejected
		}
		if err := validateBootstrapAcceptance(tenantID, currentVersion, version, currentCredential, credential, audit); err != nil {
			return err
		}
		next.WorkerVersions[versionKey] = version
		next.BootstrapCredentials[credential.TokenHash] = credential
		next.Audits[tenantID] = append(next.Audits[tenantID], audit)
		return nil
	})
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
