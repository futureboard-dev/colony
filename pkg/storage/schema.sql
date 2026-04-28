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
