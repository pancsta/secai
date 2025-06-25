CREATE TABLE characters
(
    -- IDs
    id         INTEGER PRIMARY KEY,
    session_id INTEGER,
    result     TEXT NOT NULL
);

CREATE TABLE jokes
(
    -- IDs
    id         INTEGER PRIMARY KEY,
    session_id INTEGER,
    text       TEXT NOT NULL
);

-- TODO split into resources_items
CREATE TABLE resources
(
    -- IDs
    id         INTEGER PRIMARY KEY,
    session_id INTEGER,
    key        TEXT NOT NULL,
    value      TEXT NOT NULL
);

CREATE TABLE ingredients
(
    -- IDs
    id         INTEGER PRIMARY KEY,
    session_id INTEGER,
    name       TEXT NOT NULL,
    amount     TEXT NOT NULL
);

-- TODO
-- CREATE TABLE sessions
-- (
--     id         INTEGER PRIMARY KEY,
--
--     -- content
--     stories    text     NOT NULL,
--     time_sum   int      NOT NULL,
--     serialized text     NOT NULL,
--
--     -- time
--     created_at DateTime NOT NULL
-- );
