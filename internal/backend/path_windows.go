//go:build windows

package backend

import (
	"strings"

	"golang.org/x/sys/windows"
)

// Resolve reparse points through the kernel, including junctions. The standard
// path walker does not resolve every junction form produced by PowerShell.
func realPath(path string) (string, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	handle, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", err
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	buffer := make([]uint16, 512)
	for {
		n, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
		if err != nil {
			return "", err
		}
		if int(n) >= len(buffer) {
			buffer = make([]uint16, n+1)
			continue
		}
		result := windows.UTF16ToString(buffer[:n])
		if strings.HasPrefix(result, `\\?\UNC\`) {
			return `\\` + strings.TrimPrefix(result, `\\?\UNC\`), nil
		}
		return strings.TrimPrefix(result, `\\?\`), nil
	}
}
