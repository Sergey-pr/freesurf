-- migrate:up
CREATE TABLE IF NOT EXISTS settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- migrate:down
DROP TABLE IF EXISTS settings;
