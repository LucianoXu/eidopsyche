// Package invitedb provides CRUD operations for invite tokens and their
// redemptions. The two-table schema lives in internal/store (schemaV2).
package invitedb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yingtexu/eidopsyche/internal/store"
)

// Status represents the lifecycle state of an invite.
type Status string

const (
	StatusActive  Status = "active"
	StatusExpired Status = "expired"
	StatusRevoked Status = "revoked"
)

// Invite mirrors the invites table row plus computed Uses.
type Invite struct {
	ID            string
	CreatedAt     time.Time
	ExpiresAt     time.Time // zero value = no expiry
	MaxUses       int       // 0 = unlimited
	Uses          int
	IssuerLabel   string
	RedeemerLabel string
	Status        Status
}

// Repo wraps a store.DB handle and exposes invite CRUD.
type Repo struct {
	db *store.DB
}

// New creates a new Repo backed by db.
func New(db *store.DB) *Repo { return &Repo{db: db} }

var (
	ErrNotFound        = errors.New("invite not found")
	ErrPrefixAmbiguous = errors.New("invite id prefix matches multiple records")
	ErrAlreadyRedeemed = errors.New("redeemer has already redeemed this invite")
)

// Insert stores a new invite row with status='active' and uses=0.
func (r *Repo) Insert(ctx context.Context, inv Invite) error {
	var expiresAt int64
	if !inv.ExpiresAt.IsZero() {
		expiresAt = inv.ExpiresAt.Unix()
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO invites(id, created_at, expires_at, max_uses, uses, issuer_label, redeemer_label, status)
		 VALUES(?, ?, ?, ?, 0, ?, ?, 'active')`,
		inv.ID,
		inv.CreatedAt.Unix(),
		expiresAt,
		inv.MaxUses,
		inv.IssuerLabel,
		inv.RedeemerLabel,
	)
	return err
}

// Get retrieves an invite by exact ID.
func (r *Repo) Get(ctx context.Context, id string) (*Invite, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, created_at, expires_at, max_uses, uses, issuer_label, redeemer_label, status
		 FROM invites WHERE id = ?`, id)
	return scanInvite(row)
}

// FindByPrefix returns the single invite whose ID starts with prefix.
// Returns ErrNotFound if zero matches, ErrPrefixAmbiguous if >1.
func (r *Repo) FindByPrefix(ctx context.Context, prefix string) (*Invite, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, created_at, expires_at, max_uses, uses, issuer_label, redeemer_label, status
		 FROM invites WHERE id LIKE ? || '%'`, prefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var invites []*Invite
	for rows.Next() {
		inv, err := scanInviteRow(rows)
		if err != nil {
			return nil, err
		}
		invites = append(invites, inv)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	switch len(invites) {
	case 0:
		return nil, ErrNotFound
	case 1:
		return invites[0], nil
	default:
		return nil, ErrPrefixAmbiguous
	}
}

// List returns invites filtered by status. Pass "" for all.
func (r *Repo) List(ctx context.Context, status string) ([]*Invite, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if status == "" {
		rows, err = r.db.QueryContext(ctx,
			`SELECT id, created_at, expires_at, max_uses, uses, issuer_label, redeemer_label, status
			 FROM invites ORDER BY created_at DESC`)
	} else {
		rows, err = r.db.QueryContext(ctx,
			`SELECT id, created_at, expires_at, max_uses, uses, issuer_label, redeemer_label, status
			 FROM invites WHERE status = ? ORDER BY created_at DESC`, status)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Invite
	for rows.Next() {
		inv, err := scanInviteRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

// MarkExpired sets status='expired' for the given invite ID.
func (r *Repo) MarkExpired(ctx context.Context, id string) error {
	return r.setStatus(ctx, id, StatusExpired)
}

// MarkRevoked sets status='revoked' for the given invite ID.
func (r *Repo) MarkRevoked(ctx context.Context, id string) error {
	return r.setStatus(ctx, id, StatusRevoked)
}

func (r *Repo) setStatus(ctx context.Context, id string, s Status) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE invites SET status = ? WHERE id = ?`, string(s), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// RecordRedemption inserts (invite_id, redeemerPK) into invite_redemptions and
// increments uses in a single transaction. Returns ErrAlreadyRedeemed on PK
// collision (primary key is (invite_id, redeemer_pk)). On success returns the
// Invite as it stands AFTER the increment.
func (r *Repo) RecordRedemption(ctx context.Context, id, redeemerPK string) (*Invite, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck

	_, err = tx.ExecContext(ctx,
		`INSERT INTO invite_redemptions(invite_id, redeemer_pk, redeemed_at) VALUES(?, ?, ?)`,
		id, redeemerPK, time.Now().Unix())
	if err != nil {
		if isUniqueErr(err) {
			return nil, ErrAlreadyRedeemed
		}
		return nil, fmt.Errorf("insert redemption: %w", err)
	}

	_, err = tx.ExecContext(ctx,
		`UPDATE invites SET uses = uses + 1 WHERE id = ?`, id)
	if err != nil {
		return nil, fmt.Errorf("increment uses: %w", err)
	}

	row := tx.QueryRowContext(ctx,
		`SELECT id, created_at, expires_at, max_uses, uses, issuer_label, redeemer_label, status
		 FROM invites WHERE id = ?`, id)
	inv, err := scanInvite(row)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return inv, nil
}

// scanInvite scans a single *sql.Row into an Invite.
func scanInvite(row *sql.Row) (*Invite, error) {
	var inv Invite
	var createdAt, expiresAt int64
	var status string
	err := row.Scan(
		&inv.ID,
		&createdAt,
		&expiresAt,
		&inv.MaxUses,
		&inv.Uses,
		&inv.IssuerLabel,
		&inv.RedeemerLabel,
		&status,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	inv.CreatedAt = time.Unix(createdAt, 0)
	if expiresAt != 0 {
		inv.ExpiresAt = time.Unix(expiresAt, 0)
	}
	inv.Status = Status(status)
	return &inv, nil
}

// scanInviteRow scans a *sql.Rows row into an Invite.
func scanInviteRow(rows *sql.Rows) (*Invite, error) {
	var inv Invite
	var createdAt, expiresAt int64
	var status string
	err := rows.Scan(
		&inv.ID,
		&createdAt,
		&expiresAt,
		&inv.MaxUses,
		&inv.Uses,
		&inv.IssuerLabel,
		&inv.RedeemerLabel,
		&status,
	)
	if err != nil {
		return nil, err
	}
	inv.CreatedAt = time.Unix(createdAt, 0)
	if expiresAt != 0 {
		inv.ExpiresAt = time.Unix(expiresAt, 0)
	}
	inv.Status = Status(status)
	return &inv, nil
}

func isUniqueErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") ||
		strings.Contains(msg, "PRIMARY KEY")
}
