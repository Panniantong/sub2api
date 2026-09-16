package service

import (
	"context"
	"database/sql"

	"github.com/lib/pq"
)

// Preserve a private recovery snapshot before releasing canonical login bindings.
// This reproduces UserRepository.deleteUser's binding cleanup while preserving all
// source rows internally; regular users and admin result DTOs cannot read the archive.
func cleanupArchiveIdentities(ctx context.Context, tx *sql.Tx, ids []int64, previewID string) error {
	for _, query := range []string{
		`SELECT id FROM auth_identities WHERE user_id=ANY($1) ORDER BY id FOR UPDATE`,
		`SELECT c.id FROM auth_identity_channels c JOIN auth_identities a ON a.id=c.identity_id WHERE a.user_id=ANY($1) ORDER BY c.id FOR UPDATE OF c`,
		`SELECT d.id FROM identity_adoption_decisions d JOIN auth_identities a ON a.id=d.identity_id WHERE a.user_id=ANY($1) ORDER BY d.id FOR UPDATE OF d`,
	} {
		rows, err := tx.QueryContext(ctx, query, pq.Array(ids))
		if err != nil {
			return err
		}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return err
			}
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `
 INSERT INTO user_cleanup_identity_archives(preview_id,user_id,identities,channels,adoption_decisions)
 SELECT $2::uuid,u.id,
   COALESCE((SELECT jsonb_agg(to_jsonb(a) ORDER BY a.id) FROM auth_identities a WHERE a.user_id=u.id),'[]'::jsonb),
   COALESCE((SELECT jsonb_agg(to_jsonb(c) ORDER BY c.id) FROM auth_identity_channels c JOIN auth_identities a ON a.id=c.identity_id WHERE a.user_id=u.id),'[]'::jsonb),
   COALESCE((SELECT jsonb_agg(to_jsonb(d) ORDER BY d.id) FROM identity_adoption_decisions d JOIN auth_identities a ON a.id=d.identity_id WHERE a.user_id=u.id),'[]'::jsonb)
 FROM users u WHERE u.id=ANY($1)`, pq.Array(ids), previewID)
	if err != nil {
		return err
	}
	for _, query := range []string{
		`UPDATE identity_adoption_decisions SET identity_id=NULL WHERE identity_id IN (SELECT id FROM auth_identities WHERE user_id=ANY($1))`,
		`DELETE FROM auth_identity_channels WHERE identity_id IN (SELECT id FROM auth_identities WHERE user_id=ANY($1))`,
		`DELETE FROM auth_identities WHERE user_id=ANY($1)`,
	} {
		if _, err := tx.ExecContext(ctx, query, pq.Array(ids)); err != nil {
			return err
		}
	}
	return nil
}
