package identity

import (
	"fmt"
	"os"
	"strings"
)

// LoadKey reads a private-key hex from path, constructs and returns the Keypair.
//
// Before reading the file content, LoadKey runs a platform-specific permission
// check (checkKeyACL) and refuses to load when the key file is reachable by
// principals broader than the owner. On unix the check enforces mode 0600
// (no group / world bits); on Windows it walks the file's DACL and rejects
// any allow-ACE granting access to a "wide" SID such as Everyone, Users, or
// Authenticated Users. See the platform-specific keystore_*.go files.
func LoadKey(path string) (*Keypair, error) {
	if err := checkKeyACL(path); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read key file: %w", err)
	}
	hexKey := strings.TrimSpace(string(raw))
	kp, err := FromHex(hexKey)
	if err != nil {
		return nil, fmt.Errorf("parse key: %w", err)
	}
	return kp, nil
}

// SaveKey writes the keypair's private hex to path and tightens its
// permissions so a subsequent LoadKey will accept it. On unix the file is
// chmodded to 0600; on Windows the DACL is rewritten to grant access only
// to the current user (and SYSTEM, so an Administrator can recover the host
// without having to take ownership). See protectKeyFile in the platform
// implementations.
func SaveKey(path string, k *Keypair) error {
	if err := os.WriteFile(path, []byte(k.PrivateHex+"\n"), 0o600); err != nil {
		return fmt.Errorf("write key file: %w", err)
	}
	if err := protectKeyFile(path); err != nil {
		return fmt.Errorf("protect key file: %w", err)
	}
	return nil
}
