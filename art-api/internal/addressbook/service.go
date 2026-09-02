package addressbook

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
	"github.com/google/uuid"
)

var ErrForbidden = errors.New("address book access forbidden")

const (
	PermissionRead   = "read"
	PermissionWrite  = "write"
	PermissionManage = "manage"
)

type Service struct {
	repository domain.Repository
}

func New(repository domain.Repository) *Service {
	return &Service{repository: repository}
}

func (s *Service) List(ctx context.Context, user domain.User) ([]domain.AddressBook, error) {
	books, err := s.repository.ListAddressBooks(ctx)
	if err != nil {
		return nil, err
	}
	grants, err := s.repository.ListAllAddressBookGrants(ctx)
	if err != nil {
		return nil, err
	}
	memberships, err := s.repository.ListUserGroupMemberships(ctx)
	if err != nil {
		return nil, err
	}
	grantsByBook := make(map[string][]domain.AddressBookGrant)
	for _, grant := range grants {
		grantsByBook[grant.AddressBookID] = append(grantsByBook[grant.AddressBookID], grant)
	}
	result := make([]domain.AddressBook, 0, len(books))
	for _, book := range books {
		permission, permissionErr := permissionFrom(user, book, grantsByBook[book.ID], memberships)
		if errors.Is(permissionErr, ErrForbidden) {
			continue
		}
		if permissionErr != nil {
			return nil, permissionErr
		}
		book.Permission = permission
		book.CanManage = permission == PermissionManage
		result = append(result, book)
	}
	return result, nil
}

func (s *Service) Authorize(ctx context.Context, user domain.User, bookID, required string) (domain.AddressBook, error) {
	book, err := s.repository.FindAddressBookByID(ctx, bookID)
	if err != nil {
		return book, err
	}
	permission, err := s.permission(ctx, user, book)
	if err != nil {
		return book, err
	}
	if permissionRank(permission) < permissionRank(required) {
		return book, ErrForbidden
	}
	book.Permission = permission
	book.CanManage = permission == PermissionManage
	return book, nil
}

func (s *Service) Grants(ctx context.Context, user domain.User, bookID string) ([]domain.AddressBookGrant, error) {
	book, err := s.Authorize(ctx, user, bookID, PermissionManage)
	if err != nil {
		return nil, err
	}
	if book.Kind != "shared" {
		return nil, ErrForbidden
	}
	return s.repository.ListAddressBookGrants(ctx, bookID)
}

func (s *Service) PutGrant(ctx context.Context, user domain.User, grant domain.AddressBookGrant) (domain.AddressBookGrant, error) {
	book, err := s.Authorize(ctx, user, grant.AddressBookID, PermissionManage)
	if err != nil {
		return grant, err
	}
	if book.Kind != "shared" || (grant.SubjectType != "user" && grant.SubjectType != "user_group") ||
		(grant.Permission != PermissionRead && grant.Permission != PermissionWrite) {
		return grant, ErrForbidden
	}
	grant.SubjectID = strings.TrimSpace(grant.SubjectID)
	if grant.SubjectID == "" || grant.SubjectID == book.OwnerUserID {
		return grant, ErrForbidden
	}
	if grant.SubjectType == "user" {
		if _, err := s.repository.FindUserByID(ctx, grant.SubjectID); err != nil {
			return grant, err
		}
	} else {
		group, err := s.repository.FindGroupByID(ctx, grant.SubjectID)
		if err != nil {
			return grant, err
		}
		if group.Kind != domain.GroupKindUser {
			return grant, ErrForbidden
		}
	}
	now := time.Now().UTC()
	grant.ID = uuid.NewString()
	grant.CreatedAt, grant.UpdatedAt = now, now
	if err := s.repository.UpsertAddressBookGrant(ctx, grant); err != nil {
		return grant, err
	}
	grants, err := s.repository.ListAddressBookGrants(ctx, grant.AddressBookID)
	if err != nil {
		return grant, err
	}
	for _, current := range grants {
		if current.SubjectType == grant.SubjectType && current.SubjectID == grant.SubjectID {
			return current, nil
		}
	}
	return grant, nil
}

func (s *Service) DeleteGrant(ctx context.Context, user domain.User, bookID, grantID string) error {
	book, err := s.Authorize(ctx, user, bookID, PermissionManage)
	if err != nil {
		return err
	}
	if book.Kind != "shared" {
		return ErrForbidden
	}
	return s.repository.DeleteAddressBookGrant(ctx, bookID, grantID)
}

func (s *Service) permission(ctx context.Context, user domain.User, book domain.AddressBook) (string, error) {
	if user.Role == domain.RoleAdmin || user.ID == book.OwnerUserID {
		return PermissionManage, nil
	}
	if book.Kind != "shared" {
		return "", ErrForbidden
	}
	grants, err := s.repository.ListAddressBookGrants(ctx, book.ID)
	if err != nil {
		return "", err
	}
	memberships, err := s.repository.ListUserGroupMemberships(ctx)
	if err != nil {
		return "", err
	}
	return permissionFrom(user, book, grants, memberships)
}

func permissionFrom(user domain.User, book domain.AddressBook, grants []domain.AddressBookGrant, memberships []domain.UserGroupMembership) (string, error) {
	if user.Role == domain.RoleAdmin || user.ID == book.OwnerUserID {
		return PermissionManage, nil
	}
	if book.Kind != "shared" {
		return "", ErrForbidden
	}
	groups := make(map[string]struct{})
	for _, membership := range memberships {
		if membership.Active && membership.UserID == user.ID {
			groups[membership.GroupID] = struct{}{}
		}
	}
	best := ""
	for _, grant := range grants {
		matches := grant.SubjectType == "user" && grant.SubjectID == user.ID
		if grant.SubjectType == "user_group" {
			_, matches = groups[grant.SubjectID]
		}
		if matches && permissionRank(grant.Permission) > permissionRank(best) {
			best = grant.Permission
		}
	}
	if best == "" {
		return "", ErrForbidden
	}
	return best, nil
}

func permissionRank(value string) int {
	switch value {
	case PermissionRead:
		return 1
	case PermissionWrite:
		return 2
	case PermissionManage:
		return 3
	default:
		return 0
	}
}
