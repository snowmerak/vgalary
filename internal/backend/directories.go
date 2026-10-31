package backend

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

type directoryEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type directoryPage struct {
	Path    string           `json:"path"`
	Parent  string           `json:"parent"`
	Home    string           `json:"home"`
	Roots   []string         `json:"roots"`
	Entries []directoryEntry `json:"entries"`
	Total   int              `json:"total"`
	Offset  int              `json:"offset"`
	Limit   int              `json:"limit"`
}

func directoryError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, os.ErrNotExist) {
		status = http.StatusNotFound
	} else if errors.Is(err, os.ErrPermission) {
		status = http.StatusForbidden
	}
	apiError(w, status, err)
}

// listDirectories reads one level only. Links are resolved when opening a folder,
// so navigating a junction never initiates an unbounded recursive walk.
func listDirectories(w http.ResponseWriter, r *http.Request) {
	offset, limit := 0, 200
	for key, destination := range map[string]*int{"offset": &offset, "limit": &limit} {
		if value := r.URL.Query().Get(key); value != "" {
			number, err := strconv.Atoi(value)
			if err != nil || number < 0 || (key == "limit" && (number == 0 || number > 500)) {
				apiError(w, http.StatusBadRequest, errors.New("invalid directory pagination"))
				return
			}
			*destination = number
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		apiError(w, http.StatusInternalServerError, err)
		return
	}
	requested := r.URL.Query().Get("path")
	if requested == "" {
		requested = home
	}
	path, err := filepath.Abs(requested)
	if err == nil {
		path, err = realPath(path)
	}
	if err != nil {
		directoryError(w, err)
		return
	}
	folder, err := os.Open(path)
	if err != nil {
		directoryError(w, err)
		return
	}
	defer func() { _ = folder.Close() }()
	info, err := folder.Stat()
	if err != nil {
		directoryError(w, err)
		return
	}
	if !info.IsDir() {
		apiError(w, http.StatusBadRequest, errors.New("path must be a directory"))
		return
	}
	names := []string{}
	for {
		if r.Context().Err() != nil {
			return
		}
		entries, readErr := folder.ReadDir(256)
		for _, entry := range entries {
			isDirectory := entry.IsDir()
			if !isDirectory && entry.Type()&(os.ModeSymlink|os.ModeIrregular) != 0 {
				// Windows junctions can be reported as irregular reparse points.
				resolved, resolveErr := realPath(filepath.Join(path, entry.Name()))
				if resolveErr == nil {
					target, statErr := os.Stat(resolved)
					isDirectory = statErr == nil && target.IsDir()
				}
			}
			if isDirectory {
				names = append(names, entry.Name())
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			directoryError(w, readErr)
			return
		}
	}
	slices.SortFunc(names, func(a, b string) int {
		if comparison := strings.Compare(strings.ToLower(a), strings.ToLower(b)); comparison != 0 {
			return comparison
		}
		return strings.Compare(a, b)
	})
	parent := filepath.Dir(path)
	if parent == path {
		parent = ""
	}
	page := directoryPage{Path: path, Parent: parent, Home: home, Roots: directoryRoots(home), Total: len(names), Offset: offset, Limit: limit, Entries: []directoryEntry{}}
	start := min(offset, len(names))
	end := start + min(limit, len(names)-start)
	for _, name := range names[start:end] {
		page.Entries = append(page.Entries, directoryEntry{Name: name, Path: filepath.Join(path, name)})
	}
	writeJSON(w, http.StatusOK, page)
}
