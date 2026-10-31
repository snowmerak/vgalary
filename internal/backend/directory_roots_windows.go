//go:build windows

package backend

import "golang.org/x/sys/windows"

func directoryRoots(home string) []string {
	roots := []string{home}
	drives, err := windows.GetLogicalDrives()
	if err != nil {
		return roots
	}
	for index := 0; index < 26; index++ {
		if drives&(1<<index) != 0 {
			roots = append(roots, string(rune('A'+index))+`:\`)
		}
	}
	return roots
}
