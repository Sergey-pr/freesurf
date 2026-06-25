-- migrate:up
CREATE TABLE IF NOT EXISTS servers (
    id         INTEGER PRIMARY KEY,
    name       TEXT    NOT NULL,
    kind       TEXT    NOT NULL DEFAULT 'subscription',
    url        TEXT,
    created_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS nodes (
    id         INTEGER PRIMARY KEY,
    server_id  INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    name       TEXT    NOT NULL,
    uri        TEXT    NOT NULL,
    protocol   TEXT    NOT NULL DEFAULT '',
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_nodes_server_id ON nodes(server_id);

-- migrate:down
DROP TABLE IF EXISTS nodes;
DROP TABLE IF EXISTS servers;
