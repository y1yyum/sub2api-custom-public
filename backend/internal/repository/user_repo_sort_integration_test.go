//go:build integration

package repository

import (
	"sort"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (s *UserRepoSuite) mustInsertUsageLog(userID int64, createdAt time.Time) {
	s.T().Helper()

	account := mustCreateAccount(s.T(), s.client, &service.Account{Name: "usage-log-account"})
	apiKey := mustCreateApiKey(s.T(), s.client, &service.APIKey{UserID: userID})

	_, err := integrationDB.ExecContext(
		s.ctx,
		`INSERT INTO usage_logs (user_id, api_key_id, account_id, model, input_tokens, output_tokens, total_cost, actual_cost, created_at)
		 VALUES ($1, $2, $3, 'gpt-test', 1, 1, 0.01, 0.01, $4)`,
		userID,
		apiKey.ID,
		account.ID,
		createdAt.UTC(),
	)
	s.Require().NoError(err)
}

func (s *UserRepoSuite) TestListWithFilters_SortByEmailAsc() {
	s.mustCreateUser(&service.User{Email: "z-last@example.com", Username: "z-user"})
	s.mustCreateUser(&service.User{Email: "a-first@example.com", Username: "a-user"})

	users, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{
		Page:      1,
		PageSize:  10,
		SortBy:    "email",
		SortOrder: "asc",
	}, service.UserListFilters{})
	s.Require().NoError(err)
	s.Require().Len(users, 2)
	s.Require().Equal("a-first@example.com", users[0].Email)
	s.Require().Equal("z-last@example.com", users[1].Email)
}

func (s *UserRepoSuite) mustSetCreatedAt(userID int64, createdAt time.Time) {
	s.T().Helper()

	_, err := integrationDB.ExecContext(s.ctx, `UPDATE users SET created_at = $1 WHERE id = $2`, createdAt.UTC(), userID)
	s.Require().NoError(err)
}

// mustCreateUsersOldestWithHighestID creates users whose creation order is the
// reverse of their ID order, so an ID-based sort can never pass for a
// created_at-based one. Returned oldest first.
func (s *UserRepoSuite) mustCreateUsersOldestWithHighestID(emails ...string) []*service.User {
	s.T().Helper()

	users := make([]*service.User, 0, len(emails))
	for _, email := range emails {
		users = append(users, s.mustCreateUser(&service.User{Email: email}))
	}
	sort.Slice(users, func(i, j int) bool { return users[i].ID > users[j].ID })
	base := time.Now().Add(-time.Duration(len(users)) * time.Hour).UTC().Truncate(time.Microsecond)
	for i, u := range users {
		s.mustSetCreatedAt(u.ID, base.Add(time.Duration(i)*time.Hour))
	}
	return users
}

func listedIDs(users []service.User) []int64 {
	ids := make([]int64, len(users))
	for i, u := range users {
		ids[i] = u.ID
	}
	return ids
}

func (s *UserRepoSuite) TestList_DefaultSortByNewestFirst() {
	created := s.mustCreateUsersOldestWithHighestID("a@example.com", "b@example.com", "c@example.com")

	users, _, err := s.repo.List(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 10})
	s.Require().NoError(err)
	s.Require().Equal([]int64{created[2].ID, created[1].ID, created[0].ID}, listedIDs(users))
}

func (s *UserRepoSuite) TestList_DefaultSortAscByOldestFirst() {
	created := s.mustCreateUsersOldestWithHighestID("a@example.com", "b@example.com", "c@example.com")

	users, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{
		Page:      1,
		PageSize:  10,
		SortOrder: "asc",
	}, service.UserListFilters{})
	s.Require().NoError(err)
	s.Require().Equal([]int64{created[0].ID, created[1].ID, created[2].ID}, listedIDs(users))
}

func (s *UserRepoSuite) TestList_DefaultSortBreaksCreatedAtTiesByIDDesc() {
	first := s.mustCreateUser(&service.User{Email: "tie-a@example.com"})
	second := s.mustCreateUser(&service.User{Email: "tie-b@example.com"})
	same := time.Now().Add(-time.Hour).UTC().Truncate(time.Microsecond)
	s.mustSetCreatedAt(first.ID, same)
	s.mustSetCreatedAt(second.ID, same)

	users, _, err := s.repo.List(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 10})
	s.Require().NoError(err)
	high, low := first.ID, second.ID
	if low > high {
		high, low = low, high
	}
	s.Require().Equal([]int64{high, low}, listedIDs(users))
}

func (s *UserRepoSuite) TestListWithFilters_SortByIDStillSortsByID() {
	created := s.mustCreateUsersOldestWithHighestID("a@example.com", "b@example.com")

	users, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{
		Page:      1,
		PageSize:  10,
		SortBy:    "id",
		SortOrder: "desc",
	}, service.UserListFilters{})
	s.Require().NoError(err)
	s.Require().Equal([]int64{created[0].ID, created[1].ID}, listedIDs(users))
}

func (s *UserRepoSuite) TestCreateAndRead_PreservesSignupSourceAndActivityTimestamps() {
	lastLoginAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Microsecond)
	lastActiveAt := time.Now().Add(-30 * time.Minute).UTC().Truncate(time.Microsecond)

	created := s.mustCreateUser(&service.User{
		Email:        "identity-meta@example.com",
		SignupSource: "linuxdo",
		LastLoginAt:  &lastLoginAt,
		LastActiveAt: &lastActiveAt,
	})

	got, err := s.repo.GetByID(s.ctx, created.ID)
	s.Require().NoError(err)
	s.Require().Equal("linuxdo", got.SignupSource)
	s.Require().NotNil(got.LastLoginAt)
	s.Require().NotNil(got.LastActiveAt)
	s.Require().True(got.LastLoginAt.Equal(lastLoginAt))
	s.Require().True(got.LastActiveAt.Equal(lastActiveAt))
}

func (s *UserRepoSuite) TestUpdate_PersistsSignupSourceAndActivityTimestamps() {
	created := s.mustCreateUser(&service.User{Email: "identity-update@example.com"})
	lastLoginAt := time.Now().Add(-90 * time.Minute).UTC().Truncate(time.Microsecond)
	lastActiveAt := time.Now().Add(-15 * time.Minute).UTC().Truncate(time.Microsecond)

	created.SignupSource = "oidc"
	created.LastLoginAt = &lastLoginAt
	created.LastActiveAt = &lastActiveAt

	s.Require().NoError(s.repo.Update(s.ctx, created, service.UserUpdateFields{SignupSource: true, LastLoginAt: true, LastActiveAt: true}))

	got, err := s.repo.GetByID(s.ctx, created.ID)
	s.Require().NoError(err)
	s.Require().Equal("oidc", got.SignupSource)
	s.Require().NotNil(got.LastLoginAt)
	s.Require().NotNil(got.LastActiveAt)
	s.Require().True(got.LastLoginAt.Equal(lastLoginAt))
	s.Require().True(got.LastActiveAt.Equal(lastActiveAt))
}

func (s *UserRepoSuite) TestListWithFilters_SortByLastActiveAtAsc() {
	earlier := time.Now().Add(-3 * time.Hour).UTC().Truncate(time.Microsecond)
	later := time.Now().Add(-45 * time.Minute).UTC().Truncate(time.Microsecond)

	s.mustCreateUser(&service.User{Email: "nil-active@example.com"})
	s.mustCreateUser(&service.User{Email: "later-active@example.com", LastActiveAt: &later})
	s.mustCreateUser(&service.User{Email: "earlier-active@example.com", LastActiveAt: &earlier})

	users, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{
		Page:      1,
		PageSize:  10,
		SortBy:    "last_active_at",
		SortOrder: "asc",
	}, service.UserListFilters{})
	s.Require().NoError(err)
	s.Require().Len(users, 3)
	s.Require().Equal("earlier-active@example.com", users[0].Email)
	s.Require().Equal("later-active@example.com", users[1].Email)
	s.Require().Equal("nil-active@example.com", users[2].Email)
}

func (s *UserRepoSuite) TestGetLatestUsedAtByUserIDs_UsesUsageLogs() {
	older := time.Now().Add(-4 * time.Hour).UTC().Truncate(time.Second)
	newer := time.Now().Add(-90 * time.Minute).UTC().Truncate(time.Second)

	userWithUsage := s.mustCreateUser(&service.User{Email: "usage-source@example.com"})
	userWithoutUsage := s.mustCreateUser(&service.User{Email: "usage-missing@example.com"})
	s.mustInsertUsageLog(userWithUsage.ID, older)
	s.mustInsertUsageLog(userWithUsage.ID, newer)

	got, err := s.repo.GetLatestUsedAtByUserIDs(s.ctx, []int64{userWithUsage.ID, userWithoutUsage.ID})
	s.Require().NoError(err)
	s.Require().Contains(got, userWithUsage.ID)
	s.Require().NotContains(got, userWithoutUsage.ID)
	s.Require().NotNil(got[userWithUsage.ID])
	s.Require().True(got[userWithUsage.ID].Equal(newer))
}

func (s *UserRepoSuite) TestListWithFilters_SortByLastUsedAtDesc_UsesUsageLogsNotLastActiveAt() {
	lastUsedOlder := time.Now().Add(-6 * time.Hour).UTC().Truncate(time.Second)
	lastUsedNewer := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Second)
	lastActiveVeryRecent := time.Now().Add(-10 * time.Minute).UTC().Truncate(time.Second)

	nilUsage := s.mustCreateUser(&service.User{Email: "nil-last-used@example.com"})
	wrongSource := s.mustCreateUser(&service.User{
		Email:        "active-not-usage@example.com",
		LastActiveAt: &lastActiveVeryRecent,
	})
	rightSource := s.mustCreateUser(&service.User{Email: "usage-wins@example.com"})

	s.mustInsertUsageLog(wrongSource.ID, lastUsedOlder)
	s.mustInsertUsageLog(rightSource.ID, lastUsedNewer)

	users, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{
		Page:      1,
		PageSize:  10,
		SortBy:    "last_used_at",
		SortOrder: "desc",
	}, service.UserListFilters{})
	s.Require().NoError(err)
	s.Require().Len(users, 3)
	s.Require().Equal(rightSource.ID, users[0].ID)
	s.Require().Equal(wrongSource.ID, users[1].ID)
	s.Require().Equal(nilUsage.ID, users[2].ID)
}

func TestUserRepoSortSuiteSmoke(_ *testing.T) {}
