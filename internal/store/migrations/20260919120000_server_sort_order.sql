-- migrate:up
ALTER TABLE servers ADD COLUMN sort_order INTEGER NOT NULL DEFAULT 0;
UPDATE servers SET sort_order = id;

-- migrate:down
ALTER TABLE servers DROP COLUMN sort_order;
