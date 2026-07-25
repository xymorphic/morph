//go:build windows

package credential

import (
	"errors"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

func protectCredentialDirectory(path string) error {
	return protectCredentialPath(path, windows.CONTAINER_INHERIT_ACE|windows.OBJECT_INHERIT_ACE)
}

func protectCredentialFile(path string) error {
	return protectCredentialPath(path, 0)
}

func checkCredentialPermissions(path string) error {
	currentUser, err := getCurrentUserSID()
	if err != nil {
		return err
	}
	for _, candidate := range []string{path, filepath.Dir(path)} {
		descriptor, err := windows.GetNamedSecurityInfo(
			candidate,
			windows.SE_FILE_OBJECT,
			windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
		)
		if err != nil {
			return err
		}
		owner, _, err := descriptor.Owner()
		if err != nil || owner == nil || !owner.Equals(currentUser) {
			return errors.New("credential store owner is invalid")
		}
		control, _, err := descriptor.Control()
		if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
			return errors.New("credential store permissions are too broad")
		}
		dacl, _, err := descriptor.DACL()
		if err != nil || dacl == nil || dacl.AceCount != 1 {
			return errors.New("credential store permissions are too broad")
		}
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, 0, &ace); err != nil || ace == nil ||
			ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE ||
			ace.Mask&windows.GENERIC_ALL != windows.GENERIC_ALL ||
			!(*windows.SID)(unsafe.Pointer(&ace.SidStart)).Equals(currentUser) {
			return errors.New("credential store permissions are too broad")
		}
	}

	return nil
}

func protectCredentialPath(path string, inheritance uint32) error {
	userSID, err := getCurrentUserSID()
	if err != nil {
		return err
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       inheritance,
		Trustee: windows.TRUSTEE{
			TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(userSID),
		},
	}}, nil)
	if err != nil {
		return err
	}

	return windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		acl,
		nil,
	)
}

func getCurrentUserSID() (*windows.SID, error) {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return nil, err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return nil, err
	}

	return user.User.Sid.Copy()
}

func syncCredentialDirectory(string) error {
	return nil
}

func replaceCredentialFile(source, destination string) error {
	return windows.Rename(source, destination)
}
