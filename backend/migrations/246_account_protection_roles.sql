-- Preserve every existing account policy: new defaults are applied at the
-- common account creation boundary; missing old flags retain legacy meaning.
-- Promote the oldest active administrator only when no live super-admin exists.
UPDATE users SET role = 'super_admin', updated_at = NOW()
WHERE id = (
  SELECT id FROM users
  WHERE role = 'admin' AND status = 'active' AND deleted_at IS NULL
  ORDER BY id LIMIT 1
) AND NOT EXISTS (
  SELECT 1 FROM users WHERE role = 'super_admin' AND deleted_at IS NULL
);
