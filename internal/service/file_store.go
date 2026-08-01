package service

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/wu8685/org/internal/domain"
)

type fileState struct {
	Tenants          map[string]domain.Tenant          `json:"tenants"`
	Workers          map[string]domain.Worker          `json:"workers"`
	WorkerVersions   map[string]domain.WorkerVersion   `json:"workerVersions"`
	Invocations      map[string]domain.Invocation      `json:"invocations"`
	Audits           map[string][]domain.AuditRecord   `json:"audits"`
	QuotaLeases      map[string]domain.QuotaLease      `json:"quotaLeases"`
	ActionOperations map[string]domain.ActionOperation `json:"actionOperations"`
}
type FileStore struct {
	mu    sync.RWMutex
	path  string
	state fileState
}

func NewFileStore(path string) (*FileStore, error) {
	if path == "" {
		return nil, errors.New("state file path is required")
	}
	s := &FileStore{path: path, state: fileState{Tenants: map[string]domain.Tenant{}, Workers: map[string]domain.Worker{}, WorkerVersions: map[string]domain.WorkerVersion{}, Invocations: map[string]domain.Invocation{}, Audits: map[string][]domain.AuditRecord{}, QuotaLeases: map[string]domain.QuotaLease{}, ActionOperations: map[string]domain.ActionOperation{}}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &s.state); err != nil {
		return nil, err
	}
	if s.state.WorkerVersions == nil {
		s.state.WorkerVersions = map[string]domain.WorkerVersion{}
	}
	if s.state.Workers == nil {
		s.state.Workers = map[string]domain.Worker{}
	}
	if s.state.Invocations == nil {
		s.state.Invocations = map[string]domain.Invocation{}
	}
	if s.state.Tenants == nil {
		s.state.Tenants = map[string]domain.Tenant{}
	}
	if s.state.Audits == nil {
		s.state.Audits = map[string][]domain.AuditRecord{}
	}
	if s.state.QuotaLeases == nil {
		s.state.QuotaLeases = map[string]domain.QuotaLease{}
	}
	if s.state.ActionOperations == nil {
		s.state.ActionOperations = map[string]domain.ActionOperation{}
	}
	return s, nil
}

func (s *FileStore) SaveTenant(tenant domain.Tenant) error {
	if err := domain.ValidateTenant(tenant); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, existing := range s.state.Tenants {
		if existing.Slug == tenant.Slug && id != tenant.ID {
			return errors.New("tenant slug already exists")
		}
		if id == tenant.ID && existing.Slug != tenant.Slug {
			return errors.New("tenant slug is immutable")
		}
	}
	s.state.Tenants[tenant.ID] = tenant
	return s.persist()
}
func (s *FileStore) Tenant(id string) (domain.Tenant, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tenant, ok := s.state.Tenants[id]
	return tenant, ok
}
func (s *FileStore) TenantBySlug(slug string) (domain.Tenant, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, tenant := range s.state.Tenants {
		if tenant.Slug == slug {
			return tenant, true
		}
	}
	return domain.Tenant{}, false
}
func (s *FileStore) SaveWorker(tenantID string, worker domain.Worker) error {
	if tenantID == "" || worker.TenantID != tenantID || worker.Name == "" {
		return errors.New("worker tenant identity mismatch")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Workers[tenantKey(tenantID, worker.Name)] = worker
	return s.persist()
}
func (s *FileStore) Worker(tenantID, name string) (domain.Worker, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	worker, ok := s.state.Workers[tenantKey(tenantID, name)]
	return worker, ok
}
func (s *FileStore) Workers(tenantID string) []domain.Worker {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.Worker, 0)
	for _, worker := range s.state.Workers {
		if worker.TenantID == tenantID {
			out = append(out, worker)
		}
	}
	return out
}
func (s *FileStore) SaveWorkerVersion(tenantID string, d domain.WorkerVersion) error {
	if tenantID == "" || d.TenantID != tenantID {
		return errors.New("WorkerVersion tenant identity mismatch")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.WorkerVersions[tenantKey(tenantID, d.ID)] = d
	return s.persist()
}
func (s *FileStore) WorkerVersions(tenantID, workerName string) []domain.WorkerVersion {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []domain.WorkerVersion{}
	for _, d := range s.state.WorkerVersions {
		if d.TenantID == tenantID && (workerName == "" || d.WorkerName == workerName) {
			out = append(out, d)
		}
	}
	return out
}
func (s *FileStore) WorkerVersion(tenantID, workerName, version string) (domain.WorkerVersion, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, item := range s.state.WorkerVersions {
		if item.TenantID == tenantID && item.WorkerName == workerName && item.Version == version {
			return item, true
		}
	}
	return domain.WorkerVersion{}, false
}
func (s *FileStore) UpdateWorkerVersionDescription(tenantID, workerName, version string, expectedRevision int64, description string) (domain.WorkerVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, item := range s.state.WorkerVersions {
		if item.TenantID == tenantID && item.WorkerName == workerName && item.Version == version {
			if item.Revision != expectedRevision {
				return domain.WorkerVersion{}, ErrConflict
			}
			item.Description, item.Revision, item.UpdatedAt = description, item.Revision+1, time.Now().UTC()
			s.state.WorkerVersions[key] = item
			return item, s.persist()
		}
	}
	return domain.WorkerVersion{}, ErrNotFound
}
func (s *FileStore) SaveInvocation(tenantID string, i domain.Invocation) error {
	if tenantID == "" || i.TenantID != tenantID {
		return errors.New("invocation tenant identity mismatch")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Invocations[tenantKey(tenantID, i.ID)] = i
	return s.persist()
}
func (s *FileStore) Invocation(tenantID, id string) (domain.Invocation, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	i, ok := s.state.Invocations[tenantKey(tenantID, id)]
	return i, ok
}
func (s *FileStore) Invocations(tenantID string) []domain.Invocation {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.Invocation, 0)
	for _, invocation := range s.state.Invocations {
		if invocation.TenantID == tenantID {
			out = append(out, invocation)
		}
	}
	return out
}
func (s *FileStore) InvocationByIdempotency(tenantID, workerName, workflow, key string) (domain.Invocation, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, i := range s.state.Invocations {
		if i.TenantID == tenantID && i.WorkerName == workerName && i.Workflow == workflow && i.IdempotencyKey == key {
			return i, true
		}
	}
	return domain.Invocation{}, false
}

func (s *FileStore) SaveActionOperation(tenantID string, operation domain.ActionOperation) error {
	if tenantID == "" || operation.TenantID != tenantID {
		return errors.New("action operation tenant identity mismatch")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.ActionOperations[actionOperationKey(tenantID, operation.RunID, operation.RuntimeNodeID, operation.Action, operation.OperationID)] = operation
	return s.persist()
}

func (s *FileStore) ActionOperation(tenantID, runID, nodeID, action, operationID string) (domain.ActionOperation, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	operation, ok := s.state.ActionOperations[actionOperationKey(tenantID, runID, nodeID, action, operationID)]
	return operation, ok
}
func (s *FileStore) ActionOperations(tenantID, runID string) []domain.ActionOperation {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.ActionOperation, 0)
	for _, operation := range s.state.ActionOperations {
		if operation.TenantID == tenantID && (runID == "" || operation.RunID == runID) {
			out = append(out, operation)
		}
	}
	return out
}

func (s *FileStore) AppendAudit(tenantID string, record domain.AuditRecord) error {
	if tenantID == "" || record.TenantID != tenantID {
		return errors.New("audit tenant identity mismatch")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Audits[tenantID] = append(s.state.Audits[tenantID], record)
	return s.persist()
}

func (s *FileStore) Audits(tenantID string) []domain.AuditRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]domain.AuditRecord(nil), s.state.Audits[tenantID]...)
}

func (s *FileStore) persist() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".org-state-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, s.path)
}
