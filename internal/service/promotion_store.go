package service

import (
	"errors"
	"strings"

	"github.com/wu8685/org/internal/domain"
)

func validateWorkerVersionAuditCommit(tenantID string, current, candidate domain.WorkerVersion, audit domain.AuditRecord) error {
	if tenantID == "" || current.TenantID != tenantID || candidate.TenantID != tenantID {
		return errors.New("WorkerVersion tenant identity mismatch")
	}
	if current.ID == "" || current.ID != candidate.ID || current.TenantSlug != candidate.TenantSlug || current.WorkerName != candidate.WorkerName || current.Version != candidate.Version {
		return ErrConflict
	}
	if audit.ID == "" || audit.TenantID != tenantID || audit.TenantSlug != candidate.TenantSlug || audit.TargetType != "workerVersion" || audit.TargetID != candidate.ID {
		return errors.New("promotion audit identity mismatch")
	}
	if audit.PrincipalID != "bootstrap-promotion-controller" || audit.AuthenticationMethod != "internal-controller" || audit.Permission != "bootstrap:promote" || audit.AuthorizationResult != "allowed" || !strings.HasPrefix(audit.Action, "worker.version.promotion.") {
		return errors.New("promotion audit contract mismatch")
	}
	if candidate.PromotionAttemptID == "" || candidate.PromotionPhase == "" || audit.RequestID != candidate.PromotionAttemptID || audit.Action != "worker.version.promotion."+string(candidate.PromotionPhase) {
		return errors.New("promotion audit attempt or phase mismatch")
	}
	return nil
}

func (s *MemoryStore) CommitWorkerVersionAudit(tenantID string, version domain.WorkerVersion, audit domain.AuditRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := tenantKey(tenantID, version.ID)
	current, ok := s.versions[key]
	if !ok {
		return ErrNotFound
	}
	if err := validateWorkerVersionAuditCommit(tenantID, current, version, audit); err != nil {
		return err
	}
	s.versions[key] = version
	s.audits[tenantID] = append(s.audits[tenantID], audit)
	return nil
}

func (s *FileStore) CommitWorkerVersionAudit(tenantID string, version domain.WorkerVersion, audit domain.AuditRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mutate(func(next *fileState) error {
		key := tenantKey(tenantID, version.ID)
		current, ok := next.WorkerVersions[key]
		if !ok {
			return ErrNotFound
		}
		if err := validateWorkerVersionAuditCommit(tenantID, current, version, audit); err != nil {
			return err
		}
		next.WorkerVersions[key] = version
		next.WorkerVersionRouting[key] = captureWorkerVersionRouting(version)
		next.Audits[tenantID] = append(next.Audits[tenantID], audit)
		return nil
	})
}
