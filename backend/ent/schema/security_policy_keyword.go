package schema

import (
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// SecurityPolicyKeyword 是分组安全策略的自定义关键词。
// group_id 为 NULL 表示全局词（对所有开启安全策略的分组生效），
// 非 NULL 表示仅对指定分组生效。关键词与内置 seed 词包合并后参与匹配。
type SecurityPolicyKeyword struct {
	ent.Schema
}

func (SecurityPolicyKeyword) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "security_policy_keywords"},
	}
}

func (SecurityPolicyKeyword) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
		mixins.SoftDeleteMixin{},
	}
}

func (SecurityPolicyKeyword) Fields() []ent.Field {
	return []ent.Field{
		// group_id: NULL = 全局词，非 NULL = 仅指定分组生效。
		field.Int64("group_id").
			Optional().
			Nillable(),
		field.String("keyword").
			MaxLen(200).
			NotEmpty().
			Comment("关键词原文，匹配时大小写不敏感"),
		field.String("category").
			MaxLen(50).
			Default("").
			Comment("分类：crack/reverse/pentest/privesc/evasion/custom"),
		field.Bool("enabled").
			Default(true).
			Comment("是否参与匹配"),
	}
}

func (SecurityPolicyKeyword) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("group", Group.Type).
			Field("group_id").
			Unique(),
	}
}

func (SecurityPolicyKeyword) Indexes() []ent.Index {
	return []ent.Index{
		// 全局词按 keyword 唯一（未删除）。
		index.Fields("keyword").
			Unique().
			StorageKey("idx_secpol_kw_global_unique").
			Annotations(entsql.IndexWhere("group_id IS NULL AND deleted_at IS NULL")),
		// 分组词按 (group_id, keyword) 唯一（未删除）。
		index.Fields("group_id", "keyword").
			Unique().
			StorageKey("idx_secpol_kw_group_unique").
			Annotations(entsql.IndexWhere("group_id IS NOT NULL AND deleted_at IS NULL")),
		index.Fields("group_id"),
		index.Fields("enabled"),
		index.Fields("deleted_at"),
	}
}
