package contacts

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/store"
)

type Tier string

const (
	TierMaster       Tier = "master"
	TierFriend       Tier = "friend"
	TierAcquaintance Tier = "acquaintance"
	TierBlocked      Tier = "blocked"
)

type Contact struct {
	Pubkey    string
	Label     string
	Tier      Tier
	Notes     string
	CreatedAt time.Time
	UpdatedAt time.Time
	Relays    []string
}

type Repo struct {
	db *store.DB
}

func New(db *store.DB) *Repo { return &Repo{db: db} }

var (
	ErrNotFound = errors.New("contact not found")
	ErrExists   = errors.New("contact already exists")
)

func (r *Repo) Add(ctx context.Context, c Contact) error {
	if c.Tier == "" {
		c.Tier = TierFriend
	}
	now := time.Now().Unix()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO contacts(pubkey,label,tier,notes,created_at,updated_at) VALUES(?,?,?,?,?,?)`,
		c.Pubkey, c.Label, string(c.Tier), c.Notes, now, now)
	if err != nil {
		if isUniqueErr(err) {
			return ErrExists
		}
		return err
	}
	for i, ru := range c.Relays {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO contact_relays(pubkey,relay_url,priority) VALUES(?,?,?)`,
			c.Pubkey, ru, i); err != nil {
			return fmt.Errorf("insert relay %s: %w", ru, err)
		}
	}
	return tx.Commit()
}

func (r *Repo) Get(ctx context.Context, pubkey string) (*Contact, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT pubkey,label,tier,notes,created_at,updated_at FROM contacts WHERE pubkey=?`, pubkey)
	var c Contact
	var tier, notes string
	var created, updated int64
	err := row.Scan(&c.Pubkey, &c.Label, &tier, &notes, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	c.Tier = Tier(tier)
	c.Notes = notes
	c.CreatedAt = time.Unix(created, 0)
	c.UpdatedAt = time.Unix(updated, 0)
	c.Relays, err = r.relays(ctx, pubkey)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *Repo) relays(ctx context.Context, pubkey string) ([]string, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT relay_url FROM contact_relays WHERE pubkey=? ORDER BY priority`, pubkey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (r *Repo) List(ctx context.Context) ([]*Contact, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT pubkey FROM contacts ORDER BY label`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var pks []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		pks = append(pks, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]*Contact, 0, len(pks))
	for _, p := range pks {
		c, err := r.Get(ctx, p)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func (r *Repo) Remove(ctx context.Context, pubkey string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM contacts WHERE pubkey=?`, pubkey)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repo) AllRelaysUnion(ctx context.Context) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT DISTINCT relay_url FROM contact_relays`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// AmbiguousLabelError is returned when more than one contact shares a label.
type AmbiguousLabelError struct {
	Label   string
	Pubkeys []string
}

func (e *AmbiguousLabelError) Error() string {
	return fmt.Sprintf("multiple contacts share label %q: %v", e.Label, e.Pubkeys)
}

// GetByLabel returns the unique contact with this label.
// ErrNotFound when no match. ErrLabelAmbiguous when multiple match.
func (r *Repo) GetByLabel(ctx context.Context, label string) (*Contact, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT pubkey FROM contacts WHERE label=?`, label)
	if err != nil {
		return nil, err
	}
	var pks []string
	for rows.Next() {
		var pk string
		if err := rows.Scan(&pk); err != nil {
			rows.Close()
			return nil, err
		}
		pks = append(pks, pk)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	switch len(pks) {
	case 0:
		return nil, ErrNotFound
	case 1:
		return r.Get(ctx, pks[0])
	default:
		return nil, &AmbiguousLabelError{Label: label, Pubkeys: pks}
	}
}

func isUniqueErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") ||
		strings.Contains(msg, "PRIMARY KEY")
}
