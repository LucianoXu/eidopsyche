package identity

import (
	"fmt"
	"os"
	"strings"
)

// LoadKey reads a private-key hex from path, constructs and returns the Keypair.
// It refuses to load the key if the file's mode permits group or other access
// (i.e. anything broader than 0600), returning an error in that case.
func LoadKey(path string) (*Keypair, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat key file: %w", err)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		return nil, fmt.Errorf("keystore %s has insecure mode %04o (want 0600)", path, mode)
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

// SaveKey writes the keypair's private hex to path with mode 0600.
// It creates or truncates the file as needed.
func SaveKey(path string, k *Keypair) error {
	if err := os.WriteFile(path, []byte(k.PrivateHex+"\n"), 0o600); err != nil {
		return fmt.Errorf("write key file: %w", err)
	}
	return nil
}
