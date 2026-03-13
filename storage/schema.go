package storage

const schema = `
CREATE TABLE IF NOT EXISTS session (
	id           TEXT    PRIMARY KEY,
	cwd          TEXT    NOT NULL,
	title        TEXT    NOT NULL DEFAULT '',
	time_created INTEGER NOT NULL,
	time_updated INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS message (
	id           TEXT    PRIMARY KEY,
	session_id   TEXT    NOT NULL,
	time_created INTEGER NOT NULL,
	data         TEXT    NOT NULL
);

CREATE TABLE IF NOT EXISTS part (
	id           TEXT    PRIMARY KEY,
	message_id   TEXT    NOT NULL,
	session_id   TEXT    NOT NULL,
	time_created INTEGER NOT NULL,
	data         TEXT    NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_message_session ON message(session_id);
CREATE INDEX IF NOT EXISTS idx_part_session    ON part(session_id);
CREATE INDEX IF NOT EXISTS idx_part_message    ON part(message_id);
`
