package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type DB struct {
	db *sql.DB
}

type Session struct {
	ID          string
	CWD         string
	Title       string
	TimeCreated int64
	TimeUpdated int64
}

type Message struct {
	ID          string
	SessionID   string
	TimeCreated int64
	Data        json.RawMessage
}

type Part struct {
	ID          string
	MessageID   string
	SessionID   string
	TimeCreated int64
	Data        json.RawMessage
}

func Open(path string) (*DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	db.SetMaxOpenConns(1)

	if _, err := db.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enable WAL: %w", err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys=ON;"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enable foreign_keys: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}

	return &DB{db: db}, nil
}

func (d *DB) Close() error {
	return d.db.Close()
}

func now() int64 {
	return time.Now().UnixMilli()
}

func (d *DB) InsertSession(s Session) error {
	_, err := d.db.Exec(
		`INSERT INTO session (id, cwd, title, time_created, time_updated) VALUES (?, ?, ?, ?, ?)`,
		s.ID, s.CWD, s.Title, s.TimeCreated, s.TimeUpdated,
	)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

func (d *DB) GetSession(id string) (Session, error) {
	var s Session
	err := d.db.QueryRow(
		`SELECT id, cwd, title, time_created, time_updated FROM session WHERE id = ?`, id,
	).Scan(&s.ID, &s.CWD, &s.Title, &s.TimeCreated, &s.TimeUpdated)
	if err != nil {
		return Session{}, fmt.Errorf("get session %s: %w", id, err)
	}
	return s, nil
}

func (d *DB) UpdateSession(s Session) error {
	s.TimeUpdated = now()
	_, err := d.db.Exec(
		`UPDATE session SET cwd = ?, title = ?, time_updated = ? WHERE id = ?`,
		s.CWD, s.Title, s.TimeUpdated, s.ID,
	)
	if err != nil {
		return fmt.Errorf("update session: %w", err)
	}
	return nil
}

func (d *DB) ListSessions() ([]Session, error) {
	rows, err := d.db.Query(`SELECT id, cwd, title, time_created, time_updated FROM session ORDER BY time_created DESC`)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var s Session
		if err := rows.Scan(&s.ID, &s.CWD, &s.Title, &s.TimeCreated, &s.TimeUpdated); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

func (d *DB) InsertMessage(m Message) error {
	data, err := marshalData(m.Data)
	if err != nil {
		return err
	}
	_, err = d.db.Exec(
		`INSERT INTO message (id, session_id, time_created, data) VALUES (?, ?, ?, ?)`,
		m.ID, m.SessionID, m.TimeCreated, data,
	)
	if err != nil {
		return fmt.Errorf("insert message: %w", err)
	}
	return nil
}

func (d *DB) GetMessage(id string) (Message, error) {
	var m Message
	var data string
	err := d.db.QueryRow(
		`SELECT id, session_id, time_created, data FROM message WHERE id = ?`, id,
	).Scan(&m.ID, &m.SessionID, &m.TimeCreated, &data)
	if err != nil {
		return Message{}, fmt.Errorf("get message %s: %w", id, err)
	}
	m.Data = json.RawMessage(data)
	return m, nil
}

func (d *DB) GetMessagesBySession(sessionID string) ([]Message, error) {
	rows, err := d.db.Query(
		`SELECT id, session_id, time_created, data FROM message WHERE session_id = ? ORDER BY time_created ASC`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("get messages by session: %w", err)
	}
	defer rows.Close()

	var msgs []Message
	for rows.Next() {
		var m Message
		var data string
		if err := rows.Scan(&m.ID, &m.SessionID, &m.TimeCreated, &data); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		m.Data = json.RawMessage(data)
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

func (d *DB) InsertPart(p Part) error {
	data, err := marshalData(p.Data)
	if err != nil {
		return err
	}
	_, err = d.db.Exec(
		`INSERT INTO part (id, message_id, session_id, time_created, data) VALUES (?, ?, ?, ?, ?)`,
		p.ID, p.MessageID, p.SessionID, p.TimeCreated, data,
	)
	if err != nil {
		return fmt.Errorf("insert part: %w", err)
	}
	return nil
}

func (d *DB) GetPartsByMessage(messageID string) ([]Part, error) {
	rows, err := d.db.Query(
		`SELECT id, message_id, session_id, time_created, data FROM part WHERE message_id = ? ORDER BY time_created ASC`,
		messageID,
	)
	if err != nil {
		return nil, fmt.Errorf("get parts by message: %w", err)
	}
	defer rows.Close()
	return scanParts(rows)
}

func (d *DB) GetPartsBySession(sessionID string) ([]Part, error) {
	rows, err := d.db.Query(
		`SELECT id, message_id, session_id, time_created, data FROM part WHERE session_id = ? ORDER BY time_created ASC`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("get parts by session: %w", err)
	}
	defer rows.Close()
	return scanParts(rows)
}

func (d *DB) UpdatePart(p Part) error {
	data, err := marshalData(p.Data)
	if err != nil {
		return err
	}
	_, err = d.db.Exec(
		`UPDATE part SET data = ? WHERE id = ?`,
		data, p.ID,
	)
	if err != nil {
		return fmt.Errorf("update part: %w", err)
	}
	return nil
}

func scanParts(rows *sql.Rows) ([]Part, error) {
	var parts []Part
	for rows.Next() {
		var p Part
		var data string
		if err := rows.Scan(&p.ID, &p.MessageID, &p.SessionID, &p.TimeCreated, &data); err != nil {
			return nil, fmt.Errorf("scan part: %w", err)
		}
		p.Data = json.RawMessage(data)
		parts = append(parts, p)
	}
	return parts, rows.Err()
}

func marshalData(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "{}", nil
	}
	return string(raw), nil
}
