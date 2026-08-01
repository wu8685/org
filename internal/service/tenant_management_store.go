package service

import (
	"errors"
	"sort"

	"github.com/wu8685/org/internal/domain"
)

func tenantMemberKey(tenantID, principalID string) string {
	return tenantKey(tenantID, principalID)
}

func validateTenantCreation(tenant domain.Tenant, owner domain.TenantMember) error {
	if err := domain.ValidateTenant(tenant); err != nil {
		return err
	}
	if err := domain.ValidateTenantMember(owner); err != nil {
		return err
	}
	if owner.TenantID != tenant.ID || owner.Role != domain.TenantRoleOwner {
		return errors.New("Tenant creator must be the initial owner")
	}
	return nil
}

func countTenantOwners(members []domain.TenantMember) int {
	owners := 0
	for _, member := range members {
		if member.Role == domain.TenantRoleOwner {
			owners++
		}
	}
	return owners
}

func appendTenantAudit(audits map[string][]domain.AuditRecord, tenantID string, audit domain.AuditRecord) {
	audits[tenantID] = append(audits[tenantID], audit)
}

func (s *MemoryStore) CommitTenantCreation(tenant domain.Tenant, owner domain.TenantMember, audit domain.AuditRecord) error {
	if err := validateTenantCreation(tenant, owner); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.tenants {
		if existing.ID == tenant.ID || existing.Slug == tenant.Slug {
			return ErrConflict
		}
	}
	s.tenants[tenant.ID] = tenant
	s.tenantMembers[tenantMemberKey(tenant.ID, owner.PrincipalID)] = owner
	appendTenantAudit(s.audits, tenant.ID, audit)
	return nil
}

func (s *FileStore) CommitTenantCreation(tenant domain.Tenant, owner domain.TenantMember, audit domain.AuditRecord) error {
	if err := validateTenantCreation(tenant, owner); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.state.Tenants {
		if existing.ID == tenant.ID || existing.Slug == tenant.Slug {
			return ErrConflict
		}
	}
	return s.mutate(func(next *fileState) error {
		next.Tenants[tenant.ID] = tenant
		next.TenantMembers[tenantMemberKey(tenant.ID, owner.PrincipalID)] = owner
		appendTenantAudit(next.Audits, tenant.ID, audit)
		return nil
	})
}

func validateTenantUpdate(existing, updated domain.Tenant, expectedRevision int64) error {
	if err := domain.ValidateTenant(updated); err != nil {
		return err
	}
	if existing.ID != updated.ID || existing.Slug != updated.Slug {
		return errors.New("tenant slug is immutable")
	}
	if existing.Revision != expectedRevision || updated.Revision != expectedRevision+1 {
		return ErrConflict
	}
	return nil
}

func (s *MemoryStore) CommitTenantUpdate(updated domain.Tenant, expectedRevision int64, audit domain.AuditRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.tenants[updated.ID]
	if !ok {
		return ErrNotFound
	}
	if err := validateTenantUpdate(existing, updated, expectedRevision); err != nil {
		return err
	}
	s.tenants[updated.ID] = updated
	appendTenantAudit(s.audits, updated.ID, audit)
	return nil
}

func (s *FileStore) CommitTenantUpdate(updated domain.Tenant, expectedRevision int64, audit domain.AuditRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.state.Tenants[updated.ID]
	if !ok {
		return ErrNotFound
	}
	if err := validateTenantUpdate(existing, updated, expectedRevision); err != nil {
		return err
	}
	return s.mutate(func(next *fileState) error {
		next.Tenants[updated.ID] = updated
		appendTenantAudit(next.Audits, updated.ID, audit)
		return nil
	})
}

func validateTenantMemberCommit(existing domain.TenantMember, exists bool, member domain.TenantMember, expectedRevision int64, members []domain.TenantMember) error {
	if err := domain.ValidateTenantMember(member); err != nil {
		return err
	}
	if expectedRevision == 0 {
		if exists {
			if existing.Role == member.Role {
				return nil
			}
			return ErrConflict
		}
		if member.Revision != 1 {
			return ErrConflict
		}
		return nil
	}
	if !exists {
		return ErrNotFound
	}
	if existing.Revision != expectedRevision || member.Revision != expectedRevision+1 || existing.TenantID != member.TenantID || existing.PrincipalID != member.PrincipalID {
		return ErrConflict
	}
	if existing.Role == domain.TenantRoleOwner && member.Role != domain.TenantRoleOwner && countTenantOwners(members) == 1 {
		return ErrLastTenantOwner
	}
	return nil
}

func tenantMembersFromMap(source map[string]domain.TenantMember, tenantID string) []domain.TenantMember {
	members := make([]domain.TenantMember, 0)
	for _, member := range source {
		if member.TenantID == tenantID {
			members = append(members, member)
		}
	}
	sort.Slice(members, func(i, j int) bool { return members[i].PrincipalID < members[j].PrincipalID })
	return members
}

func (s *MemoryStore) CommitTenantMember(tenantID string, member domain.TenantMember, expectedRevision int64, audit domain.AuditRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tenants[tenantID]; !ok || member.TenantID != tenantID {
		return ErrNotFound
	}
	key := tenantMemberKey(tenantID, member.PrincipalID)
	existing, exists := s.tenantMembers[key]
	if err := validateTenantMemberCommit(existing, exists, member, expectedRevision, tenantMembersFromMap(s.tenantMembers, tenantID)); err != nil {
		return err
	}
	if exists && expectedRevision == 0 {
		return nil
	}
	s.tenantMembers[key] = member
	appendTenantAudit(s.audits, tenantID, audit)
	return nil
}

func (s *FileStore) CommitTenantMember(tenantID string, member domain.TenantMember, expectedRevision int64, audit domain.AuditRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.state.Tenants[tenantID]; !ok || member.TenantID != tenantID {
		return ErrNotFound
	}
	key := tenantMemberKey(tenantID, member.PrincipalID)
	existing, exists := s.state.TenantMembers[key]
	if err := validateTenantMemberCommit(existing, exists, member, expectedRevision, tenantMembersFromMap(s.state.TenantMembers, tenantID)); err != nil {
		return err
	}
	if exists && expectedRevision == 0 {
		return nil
	}
	return s.mutate(func(next *fileState) error {
		next.TenantMembers[key] = member
		appendTenantAudit(next.Audits, tenantID, audit)
		return nil
	})
}

func validateTenantMemberRemoval(existing domain.TenantMember, exists bool, expectedRevision int64, members []domain.TenantMember) error {
	if !exists {
		return ErrNotFound
	}
	if existing.Revision != expectedRevision {
		return ErrConflict
	}
	if existing.Role == domain.TenantRoleOwner && countTenantOwners(members) == 1 {
		return ErrLastTenantOwner
	}
	return nil
}

func (s *MemoryStore) CommitTenantMemberRemoval(tenantID, principalID string, expectedRevision int64, audit domain.AuditRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := tenantMemberKey(tenantID, principalID)
	existing, exists := s.tenantMembers[key]
	if err := validateTenantMemberRemoval(existing, exists, expectedRevision, tenantMembersFromMap(s.tenantMembers, tenantID)); err != nil {
		return err
	}
	delete(s.tenantMembers, key)
	appendTenantAudit(s.audits, tenantID, audit)
	return nil
}

func (s *FileStore) CommitTenantMemberRemoval(tenantID, principalID string, expectedRevision int64, audit domain.AuditRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := tenantMemberKey(tenantID, principalID)
	existing, exists := s.state.TenantMembers[key]
	if err := validateTenantMemberRemoval(existing, exists, expectedRevision, tenantMembersFromMap(s.state.TenantMembers, tenantID)); err != nil {
		return err
	}
	return s.mutate(func(next *fileState) error {
		delete(next.TenantMembers, key)
		appendTenantAudit(next.Audits, tenantID, audit)
		return nil
	})
}

func (s *MemoryStore) TenantMember(tenantID, principalID string) (domain.TenantMember, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	member, ok := s.tenantMembers[tenantMemberKey(tenantID, principalID)]
	return member, ok
}

func (s *FileStore) TenantMember(tenantID, principalID string) (domain.TenantMember, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	member, ok := s.state.TenantMembers[tenantMemberKey(tenantID, principalID)]
	return member, ok
}

func (s *MemoryStore) TenantMembers(tenantID string) []domain.TenantMember {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return tenantMembersFromMap(s.tenantMembers, tenantID)
}

func (s *FileStore) TenantMembers(tenantID string) []domain.TenantMember {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return tenantMembersFromMap(s.state.TenantMembers, tenantID)
}

func membershipsForPrincipal(source map[string]domain.TenantMember, principalID string) []domain.TenantMember {
	memberships := make([]domain.TenantMember, 0)
	for _, member := range source {
		if member.PrincipalID == principalID {
			memberships = append(memberships, member)
		}
	}
	sort.Slice(memberships, func(i, j int) bool { return memberships[i].TenantID < memberships[j].TenantID })
	return memberships
}

func (s *MemoryStore) TenantMembershipsForPrincipal(principalID string) []domain.TenantMember {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return membershipsForPrincipal(s.tenantMembers, principalID)
}

func (s *FileStore) TenantMembershipsForPrincipal(principalID string) []domain.TenantMember {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return membershipsForPrincipal(s.state.TenantMembers, principalID)
}
