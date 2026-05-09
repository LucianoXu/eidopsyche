//go:build windows

package identity

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows keystore protection
//
// On unix the gate's secret key is guarded by mode 0600. The same idea on
// Windows requires inspecting and rewriting the file's discretionary access
// control list (DACL) directly, because Go's file mode shim returns 0666 for
// any user-writable file regardless of the real Windows permissions.
//
// `protectKeyFile` writes a DACL that grants full control to the current
// user and to LocalSystem. We include LocalSystem so that:
//   1. an Administrator who took ownership of the host (e.g., recovery from
//      a lost user account) can still read the key without first having to
//      take ownership of the file, and
//   2. the gate daemon installed as a Windows service running under the
//      LocalSystem account (the SCM default; see internal/service/scm_windows.go)
//      can load the key the user originally created during `eidos gate init`.
// We deliberately do NOT include BUILTIN\Administrators by default — local
// admins on shared boxes shouldn't read another logged-in user's gate key
// unless they explicitly take ownership.
//
// `checkKeyACL` walks the file's DACL and rejects on any allow-ACE granting
// access to a "wide" SID (Everyone, Authenticated Users, Anonymous, the local
// Users / Guests groups). That mirrors the spirit of the unix `mode&0o077`
// check: anyone broader than the owner is enough to refuse to load the key.

// wideSIDTypes are the well-known SID types that, if present in a key file's
// DACL with any allow access, should cause LoadKey to refuse the file.
var wideSIDTypes = []windows.WELL_KNOWN_SID_TYPE{
	windows.WinWorldSid,             // Everyone (S-1-1-0)
	windows.WinAnonymousSid,         // ANONYMOUS LOGON (S-1-5-7)
	windows.WinAuthenticatedUserSid, // Authenticated Users (S-1-5-11)
	windows.WinBuiltinUsersSid,      // BUILTIN\Users
	windows.WinBuiltinGuestsSid,     // BUILTIN\Guests
}

// currentUserSID returns the SID of the user the current process is running
// as. Used both to author the protective DACL and to identify which ACEs are
// "narrow" (acceptable) during the load-time check.
func currentUserSID() (*windows.SID, error) {
	tok := windows.GetCurrentProcessToken()
	user, err := tok.GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("get token user: %w", err)
	}
	return user.User.Sid.Copy()
}

// checkKeyACL walks the file's DACL and returns an error if any allow-ACE
// grants access to a wide SID. Defaulted DACLs and absent DACLs are both
// rejected: a missing DACL means "everyone has full access" in Windows.
func checkKeyACL(path string) error {
	sd, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return fmt.Errorf("get security info %s: %w", path, err)
	}
	dacl, defaulted, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("read dacl %s: %w", path, err)
	}
	if dacl == nil {
		return fmt.Errorf("keystore %s has no DACL (fully permissive); rerun `eidos gate init` or restore from a tighter copy", path)
	}
	if defaulted {
		return fmt.Errorf("keystore %s has a defaulted DACL inherited from its parent; rerun `eidos gate init` to apply a tight DACL", path)
	}

	count := uint32(dacl.AceCount)
	for i := uint32(0); i < count; i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return fmt.Errorf("get ACE %d on %s: %w", i, path, err)
		}
		// Only allow-ACEs grant access; deny-ACEs and audit-ACEs cannot
		// broaden the principal set, so they're safe to ignore here.
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		for _, t := range wideSIDTypes {
			if sid.IsWellKnown(t) {
				return fmt.Errorf("keystore %s has DACL granting access to %s; rerun `eidos gate init` or chown to the current user", path, sid.String())
			}
		}
	}
	return nil
}

// protectKeyFile rewrites the file's DACL so that only the current user and
// LocalSystem retain access. Inheritance is suppressed (PROTECTED_DACL_*) so
// a wider parent ACL does not silently re-enter via inheritance later.
func protectKeyFile(path string) error {
	userSID, err := currentUserSID()
	if err != nil {
		return err
	}
	systemSID, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return fmt.Errorf("create LocalSystem SID: %w", err)
	}

	entries := []windows.EXPLICIT_ACCESS{
		fullControlForSID(userSID),
		fullControlForSID(systemSID),
	}
	dacl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return fmt.Errorf("build DACL: %w", err)
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, // owner
		nil, // group
		dacl,
		nil, // sacl
	); err != nil {
		return fmt.Errorf("apply DACL to %s: %w", path, err)
	}
	return nil
}

// fullControlForSID returns an EXPLICIT_ACCESS entry granting GENERIC_ALL to
// the given SID with no inheritance — the file does not need to propagate
// permissions to anything (it is a single leaf file in the state directory).
func fullControlForSID(sid *windows.SID) windows.EXPLICIT_ACCESS {
	return windows.EXPLICIT_ACCESS{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}
}
