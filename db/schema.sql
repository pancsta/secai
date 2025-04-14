CREATE TABLE prompts
(
    -- IDs
    id            INTEGER PRIMARY KEY,
    session_id    text     NOT NULL,

    -- content
    agent         text     NOT NULL,
    state         text     NOT NULL,
    system        text     NOT NULL,
    history_len    integer  NOT NULL,
    request       text     NOT NULL,
    response      text,

    -- time
    created_at    DateTime NOT NULL,
    mach_time_sum int      NOT NULL,
    mach_time     text     NOT NULL
);

CREATE INDEX session ON prompts (session_id);
