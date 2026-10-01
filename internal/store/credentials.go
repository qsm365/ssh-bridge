package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

type AgentCredential struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	TokenPrefix string     `json:"token_prefix"`
	Enabled     bool       `json:"enabled"`
	TargetIDs   []string   `json:"target_ids"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
}

func (s *Store) ListAgentCredentials(ctx context.Context) ([]AgentCredential, error) {
	rows, err := s.query(ctx, `SELECT id,name,token_prefix,enabled,created_at,updated_at,last_used_at FROM agent_credentials ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]AgentCredential, 0)
	for rows.Next() {
		var c AgentCredential
		var enabled int
		var created, updated string
		var last sql.NullString
		if err := rows.Scan(&c.ID, &c.Name, &c.TokenPrefix, &enabled, &created, &updated, &last); err != nil {
			return nil, err
		}
		c.Enabled = enabled != 0
		c.CreatedAt, c.UpdatedAt, c.LastUsedAt = parseTime(created), parseTime(updated), parseOptional(last)
		items = append(items, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range items {
		items[i].TargetIDs, err = s.CredentialTargetIDs(ctx, items[i].ID)
		if err != nil {
			return nil, err
		}
	}
	return items, nil
}

func (s *Store) CredentialTargetIDs(ctx context.Context, id string) ([]string, error) {
	rows, err := s.query(ctx, `SELECT target_id FROM agent_credential_targets WHERE agent_credential_id=? ORDER BY target_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) CreateAgentCredential(ctx context.Context, c AgentCredential, tokenHash string) error {
	return s.writeCredential(ctx, c, tokenHash, true)
}

func (s *Store) UpdateAgentCredential(ctx context.Context, c AgentCredential) error {
	return s.writeCredential(ctx, c, "", false)
}

func (s *Store) writeCredential(ctx context.Context, c AgentCredential, hash string, create bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, targetID := range c.TargetIDs {
		var found int
		err = tx.QueryRowContext(ctx, s.bind(`SELECT 1 FROM targets WHERE id=? AND deleted_at IS NULL`), targetID).Scan(&found)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
	}
	if create {
		_, err = tx.ExecContext(ctx, s.bind(`INSERT INTO agent_credentials(id,name,token_hash,token_prefix,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`), c.ID, c.Name, hash, c.TokenPrefix, enabledValue(c.Enabled), nowText(), nowText())
	} else {
		var result sql.Result
		result, err = tx.ExecContext(ctx, s.bind(`UPDATE agent_credentials SET name=?,enabled=?,updated_at=? WHERE id=?`), c.Name, enabledValue(c.Enabled), nowText(), c.ID)
		if err == nil {
			var count int64
			count, err = result.RowsAffected()
			if err == nil && count == 0 {
				return ErrNotFound
			}
		}
		if err == nil {
			_, err = tx.ExecContext(ctx, s.bind(`DELETE FROM agent_credential_targets WHERE agent_credential_id=?`), c.ID)
		}
	}
	if err != nil {
		return err
	}
	for _, targetID := range c.TargetIDs {
		if _, err = tx.ExecContext(ctx, s.bind(`INSERT INTO agent_credential_targets(agent_credential_id,target_id) VALUES(?,?)`), c.ID, targetID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) RotateAgentCredential(ctx context.Context, id, hash, prefix string) error {
	result, err := s.exec(ctx, `UPDATE agent_credentials SET token_hash=?,token_prefix=?,updated_at=? WHERE id=?`, hash, prefix, nowText(), id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) AuthenticateAgent(ctx context.Context, hash string) (string, error) {
	var id string
	err := s.queryRow(ctx, `SELECT id FROM agent_credentials WHERE token_hash=? AND enabled=1`, hash).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	_, err = s.exec(ctx, `UPDATE agent_credentials SET last_used_at=? WHERE id=?`, nowText(), id)
	return id, err
}

func (s *Store) CredentialCanAccessTarget(ctx context.Context, credentialID, targetID string) (bool, error) {
	var found int
	err := s.queryRow(ctx, `SELECT 1 FROM agent_credential_targets WHERE agent_credential_id=? AND target_id=?`, credentialID, targetID).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) SessionOwnedBy(ctx context.Context, sessionID, credentialID string) (bool, error) {
	var id sql.NullString
	err := s.queryRow(ctx, `SELECT agent_credential_id FROM agent_sessions WHERE id=?`, sessionID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return id.Valid && strings.EqualFold(id.String, credentialID), nil
}
