//go:build !windows

package backend

func directoryRoots(home string) []string {
	if home == "/" {
		return []string{home}
	}
	return []string{home, "/"}
}
