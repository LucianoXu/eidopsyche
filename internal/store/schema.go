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

CREATE VIEW IF NOT EXISTS relay_whitelist AS
  SELECT pubkey FROM contacts WHERE tier != 'blocked'
  UNION
  SELECT value AS pubkey FROM meta WHERE key = 'owner_pubkey';
`

const SchemaVersion = 1
