package service

import (
	"errors"

	"github.com/wu8685/org/internal/domain"
)

func validateCurrentWorkerVersion(tenantID string, worker domain.Worker, current, version domain.WorkerVersion, audit *domain.AuditRecord) error {
	if tenantID == "" || worker.TenantID != tenantID || version.TenantID != tenantID || worker.Name == "" || worker.Name != version.WorkerName || worker.CurrentVersion != version.Version {
		return errors.New("Current WorkerVersion tenant or Worker identity mismatch")
	}
	if current.ID == "" || current.ID != version.ID || current.TenantID != version.TenantID || current.TenantSlug != version.TenantSlug || current.WorkerName != version.WorkerName || current.Version != version.Version {
		return ErrConflict
	}
	if !version.Current || version.State != domain.WorkerVersionReady {
		return errors.New("ready Current WorkerVersion is required")
	}
	if audit != nil {
		if err := validateWorkerVersionAuditCommit(tenantID, current, version, *audit); err != nil {
			return err
		}
	}
	return nil
}

func commitCurrentWorkerVersion(versions map[string]domain.WorkerVersion, workers map[string]domain.Worker, audits map[string][]domain.AuditRecord, tenantID string, worker domain.Worker, version domain.WorkerVersion, audit *domain.AuditRecord) error {
	key := tenantKey(tenantID, version.ID)
	current, ok := versions[key]
	if !ok || current.WorkerName != version.WorkerName || current.Version != version.Version {
		return ErrNotFound
	}
	if err := validateCurrentWorkerVersion(tenantID, worker, current, version, audit); err != nil {
		return err
	}
	for itemKey, item := range versions {
		if item.TenantID == tenantID && item.WorkerName == version.WorkerName && item.ID != version.ID && item.Current {
			item.Current = false
			versions[itemKey] = item
		}
	}
	versions[key] = version
	workers[tenantKey(tenantID, worker.Name)] = worker
	if audit != nil {
		audits[tenantID] = append(audits[tenantID], *audit)
	}
	return nil
}

func (s *MemoryStore) CommitCurrentWorkerVersion(tenantID string, worker domain.Worker, version domain.WorkerVersion, audit *domain.AuditRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return commitCurrentWorkerVersion(s.versions, s.workers, s.audits, tenantID, worker, version, audit)
}

func (s *FileStore) CommitCurrentWorkerVersion(tenantID string, worker domain.Worker, version domain.WorkerVersion, audit *domain.AuditRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mutate(func(next *fileState) error {
		if err := commitCurrentWorkerVersion(next.WorkerVersions, next.Workers, next.Audits, tenantID, worker, version, audit); err != nil {
			return err
		}
		next.WorkerVersionRouting[tenantKey(tenantID, version.ID)] = captureWorkerVersionRouting(version)
		return nil
	})
}
