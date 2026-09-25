package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// AccountGroup holds the edge schema definition for the account_groups relationship.
// It stores extra fields (priority, allowed_models, created_at) and uses a composite primary key.
type AccountGroup struct {
	ent.Schema
}

func (AccountGroup) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "account_groups"},
		// Composite primary key: (account_id, group_id).
		field.ID("account_id", "group_id"),
	}
}

func (AccountGroup) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("account_id"),
		field.Int64("group_id"),
		field.Int("priority").
			Default(50),
		// allowed_models: 账号在该分组内可用的模型（支持末尾 * 通配），为空表示不限制
		field.JSON("allowed_models", []string{}).
			Optional().
			SchemaType(map[string]string{dialect.Postgres: "jsonb"}),
		field.Time("created_at").
			Immutable().
			Default(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (AccountGroup) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("account", Account.Type).
			Unique().
			Required().
			Field("account_id"),
		edge.To("group", Group.Type).
			Unique().
			Required().
			Field("group_id"),
	}
}

func (AccountGroup) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("group_id"),
		index.Fields("priority"),
	}
}
