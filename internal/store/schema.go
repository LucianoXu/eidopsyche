package store

const schemaV1 = `
CREATE TABLE IF NOT EXISTS meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS contacts (
  pubkey      TEXT PRIMARY KEY,
  label       TEXT NOT NULL,
  tier        TEXT NOT NULL DEFAULT 'friend'
                  CHECK(tier IN ('master','friend','acquaintance','blocked')),
  notes       TEXT,
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS contact_relays (
  pubkey      TEXT NOT NULL,
  relay_url   TEXT NOT NULL,
  priority    INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (pubkey, relay_url),
  FOREIGN KEY (pubkey) REFERENCES contacts(pubkey) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_contact_relays_url ON contact_relays(relay_url);

CREATE TABLE IF NOT EXISTS own_relays (
  relay_url   TEXT PRIMARY KEY,
  role        TEXT NOT NULL CHECK(role IN ('home','fallback')),
  added_at    INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS relay_state (
  relay_url     TEXT PRIMARY KEY,
  last_seen_at  INTEGER NOT NULL DEFAULT 0
);
`

const SchemaVersion = 3

const schemaV2 = `
CREATE TABLE IF NOT EXISTS invites (
  id              TEXT PRIMARY KEY,
  created_at      INTEGER NOT NULL,
  expires_at      INTEGER NOT NULL,
  max_uses        INTEGER NOT NULL,
  uses            INTEGER NOT NULL DEFAULT 0,
  issuer_label    TEXT NOT NULL,
  redeemer_label  TEXT NOT NULL,
  status          TEXT NOT NULL DEFAULT 'active'
                  CHECK(status IN ('active','expired','revoked'))
);
CREATE TABLE IF NOT EXISTS invite_redemptions (
  invite_id       TEXT NOT NULL,
  redeemer_pk     TEXT NOT NULL,
  redeemed_at     INTEGER NOT NULL,
  PRIMARY KEY (invite_id, redeemer_pk),
  FOREIGN KEY (invite_id) REFERENCES invites(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_invites_status ON invites(status, expires_at);
`

const schemaV3 = `
DROP VIEW IF EXISTS relay_whitelist;
`
