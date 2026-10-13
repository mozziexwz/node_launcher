package winsys

import (
	"fmt"
	"os"
	"sync"

	"golang.org/x/sys/windows"
)

// fileAllAccess is FILE_ALL_ACCESS: ACEs need specific rights, because
// generic rights in an ACE are not mapped when the ACL is set directly.
const fileAllAccess = 0x1F01FF

var (
	privilegesOnce sync.Once
	privilegesErr  error
)

// enableOwnershipPrivileges lets an elevated process take ownership of, and
// rewrite the DACL of, a folder another user created or locked down.
func enableOwnershipPrivileges() error {
	privilegesOnce.Do(func() {
		var token windows.Token
		if err := windows.OpenProcessToken(windows.CurrentProcess(),
			windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, &token); err != nil {
			privilegesErr = err
			return
		}
		defer token.Close()
		for _, name := range []string{"SeTakeOwnershipPrivilege", "SeRestorePrivilege"} {
			var luid windows.LUID
			p, _ := windows.UTF16PtrFromString(name)
			if err := windows.LookupPrivilegeValue(nil, p, &luid); err != nil {
				privilegesErr = err
				return
			}
			tp := windows.Tokenprivileges{PrivilegeCount: 1}
			tp.Privileges[0] = windows.LUIDAndAttributes{Luid: luid, Attributes: windows.SE_PRIVILEGE_ENABLED}
			// Without the privilege this still succeeds (the shortfall only
			// shows in the thread's last error); SetNamedSecurityInfo then
			// reports access denied, which is what the caller sees.
			if err := windows.AdjustTokenPrivileges(token, false, &tp, 0, nil, nil); err != nil {
				privilegesErr = err
				return
			}
		}
	})
	return privilegesErr
}

// SecureDirectory makes Administrators the owner of path and replaces its
// DACL with a protected one that only grants Administrators and SYSTEM, both
// inherited by everything created inside. Needs elevation.
func SecureDirectory(path string) error {
	if err := enableOwnershipPrivileges(); err != nil {
		return err
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return err
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return err
	}
	grant := func(sid *windows.SID) windows.EXPLICIT_ACCESS {
		return windows.EXPLICIT_ACCESS{
			AccessPermissions: fileAllAccess,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
		}
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{grant(admins), grant(system)}, nil)
	if err != nil {
		return err
	}
	err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		admins, nil, acl, nil)
	if err != nil {
		return fmt.Errorf("无法设置 %s 的权限：%w", path, err)
	}
	return nil
}

// IsReparsePoint reports whether path is a symbolic link, junction or other
// reparse point, without following it.
func IsReparsePoint(path string) (bool, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false, err
	}
	attrs, err := windows.GetFileAttributes(p)
	if err != nil {
		if err == windows.ERROR_FILE_NOT_FOUND || err == windows.ERROR_PATH_NOT_FOUND {
			return false, os.ErrNotExist
		}
		return false, err
	}
	return attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0, nil
}

// OpenLocked opens an existing regular file for reading and denies everyone
// else write and delete access until it is closed. It fails if another
// handle already has write or delete access, and it refuses reparse points.
func OpenLocked(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil,
		windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		windows.CloseHandle(h)
		return nil, err
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		windows.CloseHandle(h)
		return nil, fmt.Errorf("%s 不是普通文件", path)
	}
	return os.NewFile(uintptr(h), path), nil
}

// DeleteOnReboot asks Windows to delete path at the next restart; used for
// files that are still in use. Needs elevation.
func DeleteOnReboot(path string) error {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(p, nil, windows.MOVEFILE_DELAY_UNTIL_REBOOT)
}
