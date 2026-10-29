//go:build unit

// Pure upstream tests retained verbatim in the unit lane; DB originals use integration.
package repository

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestScopedAdminAccountListScopesCountAndPageInSQL(t *testing.T) {
	var countScope string
	matcher := sqlmock.QueryMatcherFunc(func(expected, actual string) error {
		if !strings.Contains(actual, `"type"`) || !strings.Contains(actual, `"account_groups"`) || !strings.Contains(actual, " OR ") {
			return fmt.Errorf("查询缺少完整规则: %s", actual)
		}
		where := actual[strings.Index(actual, " WHERE "):]
		if i := strings.Index(where, " ORDER BY "); i >= 0 {
			where = where[:i]
		}
		if i := strings.Index(where, " LIMIT "); i >= 0 {
			where = where[:i]
		}
		if expected == "count" {
			countScope = where
		} else {
			if where != countScope {
				return fmt.Errorf("分页和统计范围不同")
			}
			// 允许模型映射子字段投影，禁止读取完整 credentials。
			projected := strings.ReplaceAll(actual, `"accounts"."credentials"->'model_mapping' AS model_mapping`, "model_mapping")
			if strings.Contains(projected, `"credentials"`) || strings.Contains(projected, `"extra"`) {
				return fmt.Errorf("读取了敏感字段")
			}
		}
		return nil
	})
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(matcher))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	defer client.Close()
	repo := newAccountRepositoryWithSQL(client, db, nil)
	mock.ExpectQuery("count").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("page").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	principal := &adminaccess.Principal{Policy: adminaccess.Policy{Rules: []adminaccess.Rule{
		{Permissions: []string{"accounts.read"}, GroupIDs: []int64{1}, AccountTypes: []string{"oauth"}},
		{Permissions: []string{"accounts.read"}, GroupIDs: []int64{2}, AccountTypes: []string{"apikey"}},
	}}}
	_, total, err := repo.ScopedAdminList(adminaccess.WithPrincipal(context.Background(), principal), pagination.PaginationParams{Page: 1, PageSize: 20}, "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 {
		t.Fatal(total)
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestScopedAdminCodexCandidatesQueryIsBounded(t *testing.T) {
	matcher := sqlmock.QueryMatcherFunc(func(_, actual string) error {
		where := actual[strings.Index(actual, " WHERE "):]
		if !strings.Contains(where, "credentials") || !strings.Contains(actual, " LIMIT ") {
			return fmt.Errorf("导入候选未按身份筛选并限制数量: %s", actual)
		}
		return nil
	})
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(matcher))
	require.NoError(t, err)
	defer db.Close()
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	defer client.Close()
	repo := newAccountRepositoryWithSQL(client, db, nil)
	ctx := adminaccess.WithPrincipal(context.Background(), &adminaccess.Principal{Policy: adminaccess.Policy{Rules: []adminaccess.Rule{{Permissions: []string{"accounts.credentials"}, AccountTypes: []string{"oauth"}, AllGroups: true, IncludeUngrouped: true}}}})
	mock.ExpectQuery("候选身份").WillReturnRows(sqlmock.NewRows([]string{"id", "credentials"}))
	_, err = repo.ScopedAdminCodexCandidates(ctx, service.ScopedCodexIdentity{AccessToken: "inside", AccessTokenOnly: true})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestProxySampleSQLRandomizesWholePoolBeforeLimit(t *testing.T) {
	matcher := sqlmock.QueryMatcherFunc(func(expected, actual string) error {
		if !strings.Contains(actual, `"status"`) || !strings.Contains(actual, `"expires_at"`) {
			return fmt.Errorf("缺少可用池过滤: %s", actual)
		}
		if strings.Contains(actual, `"username"`) || strings.Contains(actual, `"password"`) {
			return fmt.Errorf("抽样不应查询认证字段: %s", actual)
		}
		if expected == "sample" {
			if !strings.Contains(actual, "ORDER BY RANDOM() LIMIT 50") || strings.Contains(actual, "OFFSET") || strings.Contains(actual, "ORDER BY \"proxies\".\"id\"") {
				return fmt.Errorf("未在全池随机后限制50: %s", actual)
			}
			if strings.Count(actual, "SELECT") != 1 {
				return fmt.Errorf("不得从预先截断的子查询抽样: %s", actual)
			}
		}
		return nil
	})
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(matcher))
	require.NoError(t, err)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	mock.ExpectQuery("count").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(20000))
	mock.ExpectQuery("sample").WillReturnRows(sqlmock.NewRows([]string{"id", "name", "host", "port", "status"}).AddRow(1234, "候选", "192.0.2.1", 8080, "active"))
	items, total, err := newProxyRepositoryWithSQL(client, db).SampleOptions(context.Background(), "region")
	require.NoError(t, err)
	require.EqualValues(t, 20000, total)
	require.Len(t, items, 1)
	require.EqualValues(t, 1234, items[0].ID)
	require.NoError(t, mock.ExpectationsWereMet())
}
