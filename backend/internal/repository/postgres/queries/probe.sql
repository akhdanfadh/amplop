-- name: LockProbeCounter :one
-- FOR UPDATE makes a concurrent write wait until this transaction ends,
-- so both never read the same total and lose an increment.
SELECT total FROM probe_counter WHERE id = 1 FOR UPDATE;

-- name: UpdateProbeCounter :exec
UPDATE probe_counter SET total = @total WHERE id = 1;

-- name: InsertProbeEvent :one
INSERT INTO probe_events (note, amount, highlighted, tag)
VALUES (@note, @amount, @highlighted, @tag)
RETURNING id, created_at;
