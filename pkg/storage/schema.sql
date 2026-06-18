CREATE TABLE IF NOT EXISTS sessions (
    id           TEXT    PRIMARY KEY,
    mission_name TEXT    NOT NULL,
    started_at   DATETIME NOT NULL,
    finished_at  DATETIME,
    status       TEXT    NOT NULL DEFAULT 'running'
);

CREATE TABLE IF NOT EXISTS steps (
    id          INTEGER  PRIMARY KEY AUTOINCREMENT,
    session_id  TEXT     NOT NULL,
    step_num    INTEGER  NOT NULL,
    sub_step    TEXT     NOT NULL DEFAULT '',
    agent_id    TEXT     NOT NULL,
    role        TEXT     NOT NULL,
    input_text  TEXT,
    output_json TEXT,
    decision    TEXT,
    duration_ms INTEGER,
    started_at  DATETIME NOT NULL,
    finished_at DATETIME,
    FOREIGN KEY (session_id) REFERENCES sessions(id)
);

CREATE TABLE IF NOT EXISTS tasks (
    id          TEXT     PRIMARY KEY,
    description TEXT     NOT NULL DEFAULT '',
    spec_path   TEXT     NOT NULL DEFAULT '',
    status      TEXT     NOT NULL DEFAULT 'open',
    branch      TEXT     NOT NULL DEFAULT '',
    pr_url      TEXT     NOT NULL DEFAULT '',
    last_feedback TEXT   NOT NULL DEFAULT '',
    created_at  DATETIME NOT NULL,
    updated_at  DATETIME NOT NULL
);

-- runs holds the structured facts for blueprint/swarm pipeline runs.
-- The raw streaming output stays in .colony/logs/; log_path points to it.
CREATE TABLE IF NOT EXISTS runs (
    id          TEXT     PRIMARY KEY,
    kind        TEXT     NOT NULL,
    project     TEXT     NOT NULL,
    language    TEXT     NOT NULL DEFAULT '',
    model       TEXT     NOT NULL DEFAULT '',
    mode        TEXT     NOT NULL DEFAULT '',
    branch      TEXT     NOT NULL DEFAULT '',
    status      TEXT     NOT NULL DEFAULT 'running',
    approved    INTEGER  NOT NULL DEFAULT 0,
    rejected    INTEGER  NOT NULL DEFAULT 0,
    log_path    TEXT     NOT NULL DEFAULT '',
    started_at  DATETIME NOT NULL,
    finished_at DATETIME
);
