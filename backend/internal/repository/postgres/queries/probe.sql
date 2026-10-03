-- name: LockCounter :one
-- FOR UPDATE makes a concurrent write wait until this transaction ends,
-- so both never read the same total and lose an increment.
SELECT total FROM probe_counter WHERE id = 1 FOR UPDATE;

-- name: UpdateCounter :exec
UPDATE probe_counter SET total = @total WHERE id = 1;

-- name: InsertEvent :one
INSERT INTO probe_events (note, amount, highlighted, tag)
VALUES (@note, @amount, @highlighted, @tag)
RETURNING id, created_at;
