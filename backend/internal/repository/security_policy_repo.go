package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	dbsecpolkw "github.com/Wei-Shaw/sub2api/ent/securitypolicykeyword"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// securityPolicyRepository 实现 service.SecurityPolicyRepository。
type securityPolicyRepository struct {
	client *dbent.Client
	sql    sqlExecutor
}

func NewSecurityPolicyRepository(client *dbent.Client, sqlDB *sql.DB) service.SecurityPolicyRepository {
	return &securityPolicyRepository{client: client, sql: sqlDB}
}

// ListEffectiveKeywords 返回对指定分组生效的自定义词：全局词（group_id IS NULL）
// 与该分组词的并集，仅含 enabled 且未删除。内置 seed 由 service 层合并。
func (r *securityPolicyRepository) ListEffectiveKeywords(ctx context.Context, groupID *int64) ([]service.SecurityPolicyKeyword, error) {
	q := r.client.SecurityPolicyKeyword.Query().
		Where(
			dbsecpolkw.Enabled(true),
			dbsecpolkw.DeletedAtIsNil(),
		)
	if groupID != nil && *groupID > 0 {
		q = q.Where(dbsecpolkw.Or(
			dbsecpolkw.GroupIDIsNil(),
			dbsecpolkw.GroupIDEQ(*groupID),
		))
	} else {
		q = q.Where(dbsecpolkw.GroupIDIsNil())
	}
	rows, err := q.
		Order(dbent.Asc(dbsecpolkw.FieldID)).
		Limit(10000).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]service.SecurityPolicyKeyword, 0, len(rows))
	for _, row := range rows {
		out = append(out, service.SecurityPolicyKeyword{
			ID:        row.ID,
			GroupID:   row.GroupID,
			Keyword:   row.Keyword,
			Category:  row.Category,
			Enabled:   row.Enabled,
			CreatedAt: row.CreatedAt,
			UpdatedAt: row.UpdatedAt,
		})
	}
	return out, nil
}

func (r *securityPolicyRepository) CreateKeyword(ctx context.Context, in service.SecurityPolicyKeywordInput) (*service.SecurityPolicyKeyword, error) {
	keyword := strings.TrimSpace(in.Keyword)
	if keyword == "" {
		return nil, fmt.Errorf("keyword must not be empty")
	}
	if len([]rune(keyword)) > 200 {
		return nil, fmt.Errorf("keyword must not exceed 200 runes")
	}
	category := strings.TrimSpace(in.Category)
	if category == "" {
		category = service.SecurityPolicyCategoryCustom
	}
	builder := r.client.SecurityPolicyKeyword.Create().
		SetKeyword(keyword).
		SetCategory(category).
		SetEnabled(true)
	if in.GroupID != nil && *in.GroupID > 0 {
		builder = builder.SetGroupID(*in.GroupID)
	}
	row, err := builder.Save(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, nil, service.ErrSecurityPolicyKeywordExists)
	}
	return &service.SecurityPolicyKeyword{
		ID:        row.ID,
		GroupID:   row.GroupID,
		Keyword:   row.Keyword,
		Category:  row.Category,
		Enabled:   row.Enabled,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}, nil
}

func (r *securityPolicyRepository) UpdateKeyword(ctx context.Context, id int64, in service.SecurityPolicyKeywordUpdate) (*service.SecurityPolicyKeyword, error) {
	builder := r.client.SecurityPolicyKeyword.UpdateOneID(id)
	updated := false
	if in.Keyword != nil {
		keyword := strings.TrimSpace(*in.Keyword)
		if keyword == "" {
			return nil, fmt.Errorf("keyword must not be empty")
		}
		if len([]rune(keyword)) > 200 {
			return nil, fmt.Errorf("keyword must not exceed 200 runes")
		}
		builder = builder.SetKeyword(keyword)
		updated = true
	}
	if in.Category != nil {
		builder = builder.SetCategory(strings.TrimSpace(*in.Category))
		updated = true
	}
	if in.Enabled != nil {
		builder = builder.SetEnabled(*in.Enabled)
		updated = true
	}
	if !updated {
		return nil, fmt.Errorf("nothing to update")
	}
	builder = builder.SetUpdatedAt(time.Now())
	row, err := builder.Save(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrSecurityPolicyKeywordNotFound, service.ErrSecurityPolicyKeywordExists)
	}
	return &service.SecurityPolicyKeyword{
		ID:        row.ID,
		GroupID:   row.GroupID,
		Keyword:   row.Keyword,
		Category:  row.Category,
		Enabled:   row.Enabled,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}, nil
}

func (r *securityPolicyRepository) DeleteKeyword(ctx context.Context, id int64) error {
	// 软删除：保留审计痕迹，匹配查询自动过滤 deleted_at。
	if err := r.client.SecurityPolicyKeyword.DeleteOneID(id).Exec(ctx); err != nil {
		return translatePersistenceError(err, service.ErrSecurityPolicyKeywordNotFound, nil)
	}
	return nil
}

func (r *securityPolicyRepository) ListKeywords(ctx context.Context, groupID *int64, includeDisabled bool) ([]service.SecurityPolicyKeyword, error) {
	q := r.client.SecurityPolicyKeyword.Query().
		Where(dbsecpolkw.DeletedAtIsNil())
	if groupID != nil {
		if *groupID == 0 {
			q = q.Where(dbsecpolkw.GroupIDIsNil())
		} else {
			q = q.Where(dbsecpolkw.GroupIDEQ(*groupID))
		}
	}
	if !includeDisabled {
		q = q.Where(dbsecpolkw.Enabled(true))
	}
	rows, err := q.
		Order(dbent.Desc(dbsecpolkw.FieldID)).
		Limit(10000).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]service.SecurityPolicyKeyword, 0, len(rows))
	for _, row := range rows {
		out = append(out, service.SecurityPolicyKeyword{
			ID:        row.ID,
			GroupID:   row.GroupID,
			Keyword:   row.Keyword,
			Category:  row.Category,
			Enabled:   row.Enabled,
			CreatedAt: row.CreatedAt,
			UpdatedAt: row.UpdatedAt,
		})
	}
	return out, nil
}
