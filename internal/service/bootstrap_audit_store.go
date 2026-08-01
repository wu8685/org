package service

import (
	"errors"
	"reflect"

	"github.com/wu8685/org/internal/domain"
)

func validateBootstrapAudits(tenantID, tenantSlug, versionID string, audits []domain.AuditRecord) error {
	if tenantID == "" || tenantSlug == "" || versionID == "" || len(audits) == 0 {
		return errors.New("bootstrap Audit identity is required")
	}
	for _, audit := range audits {
		if audit.ID == "" || audit.TenantID != tenantID || audit.TenantSlug != tenantSlug || audit.AuthorizationResult != "allowed" || audit.TargetType != "workerVersion" || audit.TargetID != versionID {
			return errors.New("bootstrap Audit identity mismatch")
		}
		if audit.Action == "worker.bootstrap.credential.issued" {
			if audit.PrincipalID != "bootstrap-controller" || audit.AuthenticationMethod != "internal-controller" || audit.Permission != "bootstrap:issue" {
				return errors.New("bootstrap credential issuance Audit identity mismatch")
			}
		} else if audit.PrincipalID != "worker-bootstrap" || audit.AuthenticationMethod != "kubernetes-tokenreview" || audit.Permission != "bootstrap:register-contract" {
			return errors.New("bootstrap registration Audit identity mismatch")
		}
	}
	return nil
}

func validateBootstrapCredentialCommit(tenantID string, current domain.BootstrapCredential, exists bool, candidate domain.BootstrapCredential, audits []domain.AuditRecord) error {
	if candidate.TokenHash == "" || candidate.Binding.TenantID != tenantID {
		return errors.New("bootstrap credential binding is required")
	}
	if err := validateBootstrapAudits(tenantID, candidate.Binding.TenantSlug, candidate.Binding.WorkerVersionID, audits); err != nil {
		return err
	}
	if !exists {
		if candidate.AcceptedAt != nil || candidate.Revoked || audits[0].Action != "worker.bootstrap.credential.issued" {
			return ErrBootstrapRejected
		}
		return nil
	}
	if current.TokenHash != candidate.TokenHash || current.Binding != candidate.Binding || (current.AcceptedAt != nil && candidate.AcceptedAt == nil) || (current.Revoked && !candidate.Revoked) {
		return ErrBootstrapConflict
	}
	return nil
}

func (s *MemoryStore) CommitBootstrapCredentialAudits(tenantID string, credential domain.BootstrapCredential, audits []domain.AuditRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, exists := s.bootstrapCredentials[credential.TokenHash]
	if err := validateBootstrapCredentialCommit(tenantID, current, exists, credential, audits); err != nil {
		return err
	}
	s.bootstrapCredentials[credential.TokenHash] = credential
	s.audits[tenantID] = append(s.audits[tenantID], audits...)
	return nil
}

func (s *FileStore) CommitBootstrapCredentialAudits(tenantID string, credential domain.BootstrapCredential, audits []domain.AuditRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mutate(func(next *fileState) error {
		current, exists := next.BootstrapCredentials[credential.TokenHash]
		if err := validateBootstrapCredentialCommit(tenantID, current, exists, credential, audits); err != nil {
			return err
		}
		next.BootstrapCredentials[credential.TokenHash] = credential
		next.Audits[tenantID] = append(next.Audits[tenantID], audits...)
		return nil
	})
}

func validateBootstrapRejection(tenantID string, currentVersion, rejectedVersion domain.WorkerVersion, currentCredential, rejectedCredential domain.BootstrapCredential, audits []domain.AuditRecord) error {
	if currentVersion.ID == "" || currentVersion.TenantID != tenantID || currentVersion.ID != rejectedVersion.ID || currentVersion.WorkerName != rejectedVersion.WorkerName || currentVersion.Version != rejectedVersion.Version {
		return ErrBootstrapRejected
	}
	expected := currentVersion
	expected.RegistrationStatus = domain.BootstrapRegistrationRejected
	if !reflect.DeepEqual(expected, rejectedVersion) || currentCredential.TokenHash == "" || currentCredential.TokenHash != rejectedCredential.TokenHash || currentCredential.Binding != rejectedCredential.Binding || currentCredential.AcceptedAt != nil || currentCredential.Revoked || !rejectedCredential.Revoked {
		return ErrBootstrapRejected
	}
	return validateBootstrapAudits(tenantID, rejectedVersion.TenantSlug, rejectedVersion.ID, audits)
}

func (s *MemoryStore) CommitBootstrapRejection(tenantID string, version domain.WorkerVersion, credential domain.BootstrapCredential, audits []domain.AuditRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	versionKey := tenantKey(tenantID, version.ID)
	currentVersion, versionExists := s.versions[versionKey]
	currentCredential, credentialExists := s.bootstrapCredentials[credential.TokenHash]
	if !versionExists || !credentialExists {
		return ErrNotFound
	}
	if err := validateBootstrapRejection(tenantID, currentVersion, version, currentCredential, credential, audits); err != nil {
		return err
	}
	s.versions[versionKey] = version
	s.bootstrapCredentials[credential.TokenHash] = credential
	s.audits[tenantID] = append(s.audits[tenantID], audits...)
	return nil
}

func (s *FileStore) CommitBootstrapRejection(tenantID string, version domain.WorkerVersion, credential domain.BootstrapCredential, audits []domain.AuditRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mutate(func(next *fileState) error {
		versionKey := tenantKey(tenantID, version.ID)
		currentVersion, versionExists := next.WorkerVersions[versionKey]
		currentCredential, credentialExists := next.BootstrapCredentials[credential.TokenHash]
		if !versionExists || !credentialExists {
			return ErrNotFound
		}
		if err := validateBootstrapRejection(tenantID, currentVersion, version, currentCredential, credential, audits); err != nil {
			return err
		}
		next.WorkerVersions[versionKey] = version
		next.WorkerVersionRouting[versionKey] = captureWorkerVersionRouting(version)
		next.BootstrapCredentials[credential.TokenHash] = credential
		next.Audits[tenantID] = append(next.Audits[tenantID], audits...)
		return nil
	})
}
