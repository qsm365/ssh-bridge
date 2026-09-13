package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")

type Store struct {
	db      *sql.DB
	dialect string
}

type Target struct {
	ID                 string    `json:"id"`
	Name               string    `json:"name"`
	Host               string    `json:"host"`
	Port               int       `json:"port"`
	SSHUser            string    `json:"ssh_user"`
	PrivateKeyPath     string    `json:"private_key_path,omitempty"`
	HostKeyFingerprint string    `json:"host_key_fingerprint"`
	Description        string    `json:"description"`
	Enabled            bool      `json:"enabled"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type AgentSession struct {
	ID             string    `json:"id"`
	Title          string    `json:"title"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	ExecutionCount int       `json:"execution_count"`
	HasError       bool      `json:"has_error"`
}

type Execution struct {
	ID            string     `json:"id"`
	SessionID     string     `json:"session_id"`
	TargetID      string     `json:"target_id"`
	TargetName    string     `json:"target_name,omitempty"`
	Title         string     `json:"title"`
	Command       string     `json:"command"`
	WorkingDir    string     `json:"working_dir"`
	Status        string     `json:"status"`
	ExitCode      *int       `json:"exit_code"`
	ErrorMessage  string     `json:"error_message,omitempty"`
	OutputPath    string     `json:"-"`
	OutputSize    int64      `json:"output_size"`
	OutputPreview string     `json:"output_preview"`
	CreatedAt     time.Time  `json:"created_at"`
	StartedAt     *time.Time `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at"`
	Artifacts     []Artifact `json:"artifacts,omitempty"`
}

type Artifact struct {
	ID           string    `json:"id"`
	ExecutionID  string    `json:"execution_id"`
	Placeholder  string    `json:"placeholder"`
	OriginalName string    `json:"original_name"`
	LocalPath    string    `json:"-"`
	RemotePath   string    `json:"remote_path,omitempty"`
	Size         int64     `json:"size"`
	SHA256       string    `json:"sha256"`
	CreatedAt    time.Time `json:"created_at"`
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return open(db, "sqlite")
}

func OpenPostgres(databaseURL string) (*Store, error) {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
	return open(db, "postgres")
}

func open(db *sql.DB, dialect string) (*Store, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect to %s database: %w", dialect, err)
	}
	s := &Store{db: db, dialect: dialect}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := s.exec(context.Background(), `UPDATE executions SET status='failed', error_message='SSH Bridge restarted before execution completed', finished_at=? WHERE status IN ('pending','running')`, nowText()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error                   { return s.db.Close() }
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

func (s *Store) bind(query string) string {
	if s.dialect != "postgres" {
		return query
	}
	var b strings.Builder
	argument := 1
	for _, character := range query {
		if character == '?' {
			fmt.Fprintf(&b, "$%d", argument)
			argument++
		} else {
			b.WriteRune(character)
		}
	}
	return b.String()
}

func (s *Store) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return s.db.ExecContext(ctx, s.bind(query), args...)
}
func (s *Store) query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return s.db.QueryContext(ctx, s.bind(query), args...)
}
func (s *Store) queryRow(ctx context.Context, query string, args ...any) *sql.Row {
	return s.db.QueryRowContext(ctx, s.bind(query), args...)
}

type migration struct {
	version    int
	statements []string
}

var migrations = []migration{{version: 1, statements: []string{
	`CREATE TABLE IF NOT EXISTS targets (
 id TEXT PRIMARY KEY, name TEXT NOT NULL, host TEXT NOT NULL, port INTEGER NOT NULL,
 ssh_user TEXT NOT NULL, private_key_path TEXT NOT NULL, host_key_fingerprint TEXT NOT NULL,
 description TEXT NOT NULL DEFAULT '', enabled INTEGER NOT NULL DEFAULT 1,
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL
)`,
	`CREATE TABLE IF NOT EXISTS agent_sessions (
 id TEXT PRIMARY KEY, title TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, updated_at TEXT NOT NULL
)`,
	`CREATE TABLE IF NOT EXISTS executions (
 id TEXT PRIMARY KEY, session_id TEXT NOT NULL REFERENCES agent_sessions(id),
 target_id TEXT NOT NULL REFERENCES targets(id), title TEXT NOT NULL DEFAULT '', command TEXT NOT NULL,
 working_dir TEXT NOT NULL DEFAULT '', status TEXT NOT NULL, exit_code INTEGER, error_message TEXT NOT NULL DEFAULT '',
 output_path TEXT NOT NULL DEFAULT '', output_size INTEGER NOT NULL DEFAULT 0, output_preview TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL, started_at TEXT, finished_at TEXT
)`,
	`CREATE TABLE IF NOT EXISTS execution_artifacts (
 id TEXT PRIMARY KEY, execution_id TEXT NOT NULL REFERENCES executions(id),
 placeholder TEXT NOT NULL, original_name TEXT NOT NULL, local_path TEXT NOT NULL,
 remote_path TEXT NOT NULL DEFAULT '', size INTEGER NOT NULL, sha256 TEXT NOT NULL,
 created_at TEXT NOT NULL, UNIQUE(execution_id, placeholder)
)`,
	`CREATE INDEX IF NOT EXISTS idx_executions_session_created ON executions(session_id, created_at)`,
	`CREATE INDEX IF NOT EXISTS idx_executions_created ON executions(created_at DESC)`,
}}}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return fmt.Errorf("create migration table: %w", err)
	}
	for _, item := range migrations {
		var exists int
		err := s.queryRow(ctx, `SELECT 1 FROM schema_migrations WHERE version=?`, item.version).Scan(&exists)
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("check migration %d: %w", item.version, err)
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		for _, statement := range item.statements {
			if _, err = tx.ExecContext(ctx, statement); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("apply migration %d: %w", item.version, err)
			}
		}
		if _, err = tx.ExecContext(ctx, s.bind(`INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)`), item.version, nowText()); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %d: %w", item.version, err)
		}
		if err = tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", item.version, err)
		}
	}
	return nil
}

func enabledValue(enabled bool) int {
	if enabled {
		return 1
	}
	return 0
}

func nowText() string              { return time.Now().UTC().Format(time.RFC3339Nano) }
func parseTime(v string) time.Time { t, _ := time.Parse(time.RFC3339Nano, v); return t }
func parseOptional(v sql.NullString) *time.Time {
	if !v.Valid {
		return nil
	}
	t := parseTime(v.String)
	return &t
}

func (s *Store) ListTargets(ctx context.Context) ([]Target, error) {
	rows, err := s.query(ctx, `SELECT id,name,host,port,ssh_user,private_key_path,host_key_fingerprint,description,enabled,created_at,updated_at FROM targets ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Target, 0)
	for rows.Next() {
		var t Target
		var enabled int
		var created, updated string
		if err := rows.Scan(&t.ID, &t.Name, &t.Host, &t.Port, &t.SSHUser, &t.PrivateKeyPath, &t.HostKeyFingerprint, &t.Description, &enabled, &created, &updated); err != nil {
			return nil, err
		}
		t.Enabled = enabled != 0
		t.CreatedAt = parseTime(created)
		t.UpdatedAt = parseTime(updated)
		items = append(items, t)
	}
	return items, rows.Err()
}

func (s *Store) GetTarget(ctx context.Context, id string) (Target, error) {
	var t Target
	var enabled int
	var created, updated string
	err := s.queryRow(ctx, `SELECT id,name,host,port,ssh_user,private_key_path,host_key_fingerprint,description,enabled,created_at,updated_at FROM targets WHERE id=?`, id).Scan(&t.ID, &t.Name, &t.Host, &t.Port, &t.SSHUser, &t.PrivateKeyPath, &t.HostKeyFingerprint, &t.Description, &enabled, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	if err != nil {
		return t, err
	}
	t.Enabled = enabled != 0
	t.CreatedAt = parseTime(created)
	t.UpdatedAt = parseTime(updated)
	return t, nil
}

func (s *Store) SaveTarget(ctx context.Context, t Target) error {
	now := nowText()
	_, err := s.exec(ctx, `INSERT INTO targets(id,name,host,port,ssh_user,private_key_path,host_key_fingerprint,description,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, t.ID, t.Name, t.Host, t.Port, t.SSHUser, t.PrivateKeyPath, t.HostKeyFingerprint, t.Description, enabledValue(t.Enabled), now, now)
	return err
}

func (s *Store) UpdateTarget(ctx context.Context, t Target) error {
	r, err := s.exec(ctx, `UPDATE targets SET name=?,host=?,port=?,ssh_user=?,private_key_path=?,host_key_fingerprint=?,description=?,enabled=?,updated_at=? WHERE id=?`, t.Name, t.Host, t.Port, t.SSHUser, t.PrivateKeyPath, t.HostKeyFingerprint, t.Description, enabledValue(t.Enabled), nowText(), t.ID)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) CreateExecution(ctx context.Context, session AgentSession, e Execution, artifacts []Artifact, createSession bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := e.CreatedAt.UTC().Format(time.RFC3339Nano)
	if createSession {
		if _, err = tx.ExecContext(ctx, s.bind(`INSERT INTO agent_sessions(id,title,created_at,updated_at) VALUES(?,?,?,?)`), session.ID, session.Title, now, now); err != nil {
			return err
		}
	} else {
		var exists int
		if err = tx.QueryRowContext(ctx, s.bind(`SELECT 1 FROM agent_sessions WHERE id=?`), e.SessionID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, s.bind(`UPDATE agent_sessions SET updated_at=? WHERE id=?`), now, e.SessionID)
		if err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, s.bind(`INSERT INTO executions(id,session_id,target_id,title,command,working_dir,status,created_at) VALUES(?,?,?,?,?,?,?,?)`), e.ID, e.SessionID, e.TargetID, e.Title, e.Command, e.WorkingDir, e.Status, now)
	if err != nil {
		return err
	}
	for _, artifact := range artifacts {
		created := artifact.CreatedAt.UTC().Format(time.RFC3339Nano)
		if _, err = tx.ExecContext(ctx, s.bind(`INSERT INTO execution_artifacts(id,execution_id,placeholder,original_name,local_path,size,sha256,created_at) VALUES(?,?,?,?,?,?,?,?)`), artifact.ID, e.ID, artifact.Placeholder, artifact.OriginalName, artifact.LocalPath, artifact.Size, artifact.SHA256, created); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) MarkArtifactRemotePath(ctx context.Context, id, path string) error {
	_, err := s.exec(ctx, `UPDATE execution_artifacts SET remote_path=? WHERE id=?`, path, id)
	return err
}

func (s *Store) ListArtifacts(ctx context.Context, executionID string) ([]Artifact, error) {
	rows, err := s.query(ctx, `SELECT id,execution_id,placeholder,original_name,local_path,remote_path,size,sha256,created_at FROM execution_artifacts WHERE execution_id=? ORDER BY placeholder`, executionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Artifact, 0)
	for rows.Next() {
		var artifact Artifact
		var created string
		if err := rows.Scan(&artifact.ID, &artifact.ExecutionID, &artifact.Placeholder, &artifact.OriginalName, &artifact.LocalPath, &artifact.RemotePath, &artifact.Size, &artifact.SHA256, &created); err != nil {
			return nil, err
		}
		artifact.CreatedAt = parseTime(created)
		items = append(items, artifact)
	}
	return items, rows.Err()
}

func (s *Store) GetArtifact(ctx context.Context, executionID, id string) (Artifact, error) {
	var artifact Artifact
	var created string
	err := s.queryRow(ctx, `SELECT id,execution_id,placeholder,original_name,local_path,remote_path,size,sha256,created_at FROM execution_artifacts WHERE execution_id=? AND id=?`, executionID, id).Scan(&artifact.ID, &artifact.ExecutionID, &artifact.Placeholder, &artifact.OriginalName, &artifact.LocalPath, &artifact.RemotePath, &artifact.Size, &artifact.SHA256, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return artifact, ErrNotFound
	}
	if err != nil {
		return artifact, err
	}
	artifact.CreatedAt = parseTime(created)
	return artifact, nil
}

func (s *Store) MarkRunning(ctx context.Context, id, path string) error {
	_, err := s.exec(ctx, `UPDATE executions SET status='running',started_at=?,output_path=? WHERE id=?`, nowText(), path, id)
	return err
}
func (s *Store) FinishExecution(ctx context.Context, id, status string, exitCode *int, message string, size int64, preview string) error {
	_, err := s.exec(ctx, `UPDATE executions SET status=?,exit_code=?,error_message=?,output_size=?,output_preview=?,finished_at=? WHERE id=?`, status, exitCode, message, size, preview, nowText(), id)
	return err
}

func scanExecution(scanner interface{ Scan(...any) error }) (Execution, error) {
	var e Execution
	var exit sql.NullInt64
	var created string
	var started, finished sql.NullString
	err := scanner.Scan(&e.ID, &e.SessionID, &e.TargetID, &e.TargetName, &e.Title, &e.Command, &e.WorkingDir, &e.Status, &exit, &e.ErrorMessage, &e.OutputPath, &e.OutputSize, &e.OutputPreview, &created, &started, &finished)
	if err != nil {
		return e, err
	}
	if exit.Valid {
		x := int(exit.Int64)
		e.ExitCode = &x
	}
	e.CreatedAt = parseTime(created)
	e.StartedAt = parseOptional(started)
	e.FinishedAt = parseOptional(finished)
	return e, nil
}

const executionSelect = `SELECT e.id,e.session_id,e.target_id,t.name,e.title,e.command,e.working_dir,e.status,e.exit_code,e.error_message,e.output_path,e.output_size,e.output_preview,e.created_at,e.started_at,e.finished_at FROM executions e JOIN targets t ON t.id=e.target_id`

func (s *Store) GetExecution(ctx context.Context, sessionID, id string) (Execution, error) {
	e, err := scanExecution(s.queryRow(ctx, executionSelect+` WHERE e.session_id=? AND e.id=?`, sessionID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNotFound
	}
	return e, err
}

func (s *Store) GetExecutionByID(ctx context.Context, id string) (Execution, error) {
	e, err := scanExecution(s.queryRow(ctx, executionSelect+` WHERE e.id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNotFound
	}
	return e, err
}
func (s *Store) ListExecutions(ctx context.Context, limit int) ([]Execution, error) {
	rows, err := s.query(ctx, executionSelect+` ORDER BY e.created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Execution, 0)
	for rows.Next() {
		e, err := scanExecution(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, e)
	}
	return items, rows.Err()
}
func (s *Store) ListSessionExecutions(ctx context.Context, sessionID string) ([]Execution, error) {
	rows, err := s.query(ctx, executionSelect+` WHERE e.session_id=? ORDER BY e.created_at`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Execution, 0)
	for rows.Next() {
		e, err := scanExecution(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, e)
	}
	return items, rows.Err()
}

func (s *Store) ListSessions(ctx context.Context) ([]AgentSession, error) {
	rows, err := s.query(ctx, `SELECT s.id,s.title,s.created_at,s.updated_at,COUNT(e.id),COALESCE(MAX(CASE WHEN e.status IN ('failed','timeout') THEN 1 ELSE 0 END),0) FROM agent_sessions s LEFT JOIN executions e ON e.session_id=s.id GROUP BY s.id ORDER BY s.updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]AgentSession, 0)
	for rows.Next() {
		var x AgentSession
		var c, u string
		var bad int
		if err := rows.Scan(&x.ID, &x.Title, &c, &u, &x.ExecutionCount, &bad); err != nil {
			return nil, err
		}
		x.CreatedAt = parseTime(c)
		x.UpdatedAt = parseTime(u)
		x.HasError = bad != 0
		items = append(items, x)
	}
	return items, rows.Err()
}
func (s *Store) GetSession(ctx context.Context, id string) (AgentSession, error) {
	var x AgentSession
	var c, u string
	var bad int
	err := s.queryRow(ctx, `SELECT s.id,s.title,s.created_at,s.updated_at,COUNT(e.id),COALESCE(MAX(CASE WHEN e.status IN ('failed','timeout') THEN 1 ELSE 0 END),0) FROM agent_sessions s LEFT JOIN executions e ON e.session_id=s.id WHERE s.id=? GROUP BY s.id`, id).Scan(&x.ID, &x.Title, &c, &u, &x.ExecutionCount, &bad)
	if errors.Is(err, sql.ErrNoRows) {
		return x, ErrNotFound
	}
	if err != nil {
		return x, fmt.Errorf("get session: %w", err)
	}
	x.CreatedAt = parseTime(c)
	x.UpdatedAt = parseTime(u)
	x.HasError = bad != 0
	return x, nil
}
