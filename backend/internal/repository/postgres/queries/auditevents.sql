-- name: InsertAuditEvent :exec
-- A NULL actor is the host operator; a NULL target is no account.
INSERT INTO audit_events (action, actor_user_id, target_user_id)
VALUES (@action, @actor_user_id, @target_user_id);
