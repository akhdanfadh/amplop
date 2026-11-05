-- +goose Up
-- Sign-in and registration attempts count a client as clientprefix.Of does:
-- an IPv4 address alone, or an IPv6 /64. Each saved address becomes that
-- prefix, written as Go writes it, so attempts made before still count.
ALTER TABLE sign_in_attempts RENAME COLUMN client_addr TO client_prefix;
ALTER INDEX sign_in_attempts_client_addr_idx RENAME TO sign_in_attempts_client_prefix_idx;
ALTER TABLE registration_attempts RENAME COLUMN client_addr TO client_prefix;

UPDATE sign_in_attempts SET client_prefix = network(set_masklen(
    client_prefix::inet, CASE family(client_prefix::inet) WHEN 4 THEN 32 ELSE 64 END
))::text;
UPDATE registration_attempts SET client_prefix = network(set_masklen(
    client_prefix::inet, CASE family(client_prefix::inet) WHEN 4 THEN 32 ELSE 64 END
))::text;

-- +goose Down
-- A prefix keeps only its first address: the saved one for IPv4, the start
-- of the /64 for IPv6.
UPDATE sign_in_attempts SET client_prefix = host(client_prefix::cidr);
UPDATE registration_attempts SET client_prefix = host(client_prefix::cidr);

ALTER TABLE registration_attempts RENAME COLUMN client_prefix TO client_addr;
ALTER INDEX sign_in_attempts_client_prefix_idx RENAME TO sign_in_attempts_client_addr_idx;
ALTER TABLE sign_in_attempts RENAME COLUMN client_prefix TO client_addr;
