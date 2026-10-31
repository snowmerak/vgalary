package backend

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/nwaples/rardecode/v2"
)

type discoveredFile struct {
	Path     string
	Location Location
}
type scanTask struct {
	Path        string
	Location    Location
	Directories []os.FileInfo
	Archives    []string
}
type scanReport struct {
	Complete bool
	Cycles   int
	Issues   []JobIssue
}
type scanBudget struct {
	expanded int64
	entries  int
	p        ProcessingSettings
}
type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(b)
}

func mediaType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp":
		return "image"
	case ".mp4", ".mkv", ".webm", ".mov", ".avi", ".m4v", ".ts":
		return "video"
	case ".zip":
		return "zip"
	case ".rar":
		return "rar"
	}
	return ""
}
func inside(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// Resolve one path component at a time: Windows junction descendants are not
// reliably handled by a single EvalSymlinks call on all supported hosts.
func sourcePath(root, relative string) (string, error) {
	root, err := realPath(root)
	if err != nil {
		return "", err
	}
	if !inside(root, filepath.Join(root, relative)) || filepath.IsAbs(relative) || filepath.VolumeName(relative) != "" {
		return "", errors.New("path outside source")
	}
	current := root
	for _, part := range strings.Split(filepath.Clean(relative), string(filepath.Separator)) {
		if part == "." || part == "" {
			continue
		}
		current, err = realPath(filepath.Join(current, part))
		if err != nil {
			return "", err
		}
		if !inside(root, current) {
			return "", errors.New("link target outside source")
		}
	}
	return current, nil
}
func archiveName(name string) (string, error) {
	name = strings.ReplaceAll(name, `\`, "/")
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return "", errors.New("archive path traversal")
		}
	}
	if strings.HasPrefix(name, "/") || strings.Contains(name, ":") || strings.ContainsRune(name, 0) {
		return "", errors.New("invalid archive path")
	}
	cleaned := path.Clean(name)
	if cleaned == "." || cleaned == "" {
		return "", errors.New("empty archive path")
	}
	return cleaned, nil
}
func locationText(loc Location) string {
	result := loc.RelativePath
	for _, entry := range loc.ArchiveEntries {
		result += "!/" + entry.Name
	}
	return result
}
func hashFile(ctx context.Context, name string) (string, error) {
	f, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err = io.Copy(h, contextReader{ctx, f}); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func walkSource(ctx context.Context, source Source, workDir string, p ProcessingSettings, visit func(discoveredFile) error) scanReport {
	report := scanReport{Complete: true, Issues: []JobIssue{}}
	issue := func(loc Location, code string, err error) {
		report.Complete = false
		report.Issues = append(report.Issues, JobIssue{Location: locationText(loc), Code: code, Message: err.Error()})
	}
	root, err := realPath(source.RootPath)
	if err != nil {
		issue(Location{}, "source_unavailable", err)
		return report
	}
	stack := []scanTask{{Path: root, Location: Location{RelativePath: "."}}}
	budget := &scanBudget{p: p}
	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			issue(Location{}, "canceled", err)
			break
		}
		task := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		budget.entries++
		if budget.entries > p.MaxEntries {
			issue(task.Location, "resource_limit_exceeded", errors.New("entry count limit reached"))
			break
		}
		info, err := os.Stat(task.Path)
		if err != nil {
			issue(task.Location, "stat_failed", err)
			continue
		}
		if len(task.Location.ArchiveEntries) == 0 {
			resolved, err := realPath(task.Path)
			if err != nil {
				issue(task.Location, "link_unavailable", err)
				continue
			}
			if !inside(root, resolved) {
				issue(task.Location, "outside_source", errors.New("link target is outside this source"))
				continue
			}
			task.Path = resolved
		}
		if info.IsDir() {
			cycle := false
			for _, ancestor := range task.Directories {
				if os.SameFile(info, ancestor) {
					cycle = true
					break
				}
			}
			if cycle {
				report.Cycles++
				continue
			}
			children, err := os.ReadDir(task.Path)
			if err != nil {
				issue(task.Location, "directory_unavailable", err)
				continue
			}
			ancestors := append(append([]os.FileInfo(nil), task.Directories...), info)
			for i := len(children) - 1; i >= 0; i-- {
				entry := children[i]
				loc := Location{RelativePath: filepath.Join(task.Location.RelativePath, entry.Name())}
				stack = append(stack, scanTask{Path: filepath.Join(task.Path, entry.Name()), Location: loc, Directories: ancestors})
			}
			continue
		}
		if !info.Mode().IsRegular() {
			issue(task.Location, "unsupported_file_type", errors.New("not a regular file"))
			continue
		}
		kind := mediaType(task.Path)
		if kind == "zip" || kind == "rar" {
			if info.Size() > p.MaxEntryBytes {
				issue(task.Location, "resource_limit_exceeded", errors.New("archive size limit reached"))
				continue
			}
			checksum, err := hashFile(ctx, task.Path)
			if err != nil {
				issue(task.Location, "archive_read_failed", err)
				continue
			}
			cycle := false
			for _, ancestor := range task.Archives {
				if ancestor == checksum {
					cycle = true
					break
				}
			}
			if cycle {
				report.Cycles++
				continue
			}
			task.Archives = append(append([]string(nil), task.Archives...), checksum)
			children, err := expandArchive(ctx, task, kind, workDir, budget)
			if err != nil {
				issue(task.Location, "archive_read_failed", err)
				continue
			}
			for i := len(children) - 1; i >= 0; i-- {
				stack = append(stack, children[i])
			}
			continue
		}
		if kind == "" {
			continue
		}
		if info.Size() > p.MaxEntryBytes {
			issue(task.Location, "resource_limit_exceeded", errors.New("media size limit reached"))
			continue
		}
		if err := visit(discoveredFile{Path: task.Path, Location: task.Location}); err != nil {
			issue(task.Location, "index_failed", err)
		}
	}
	return report
}

func (b *scanBudget) copyEntry(ctx context.Context, r io.Reader, destination io.Writer) (int64, error) {
	limit := min(b.p.MaxEntryBytes, b.p.MaxExpandedBytes-b.expanded)
	if limit < 0 {
		return 0, errors.New("resource_limit_exceeded: expanded byte budget")
	}
	n, err := io.Copy(destination, io.LimitReader(contextReader{ctx, r}, limit+1))
	b.expanded += n
	if err != nil {
		return n, err
	}
	if n > limit {
		return n, errors.New("resource_limit_exceeded: expanded byte budget")
	}
	return n, nil
}

func expandArchive(ctx context.Context, parent scanTask, kind, workDir string, budget *scanBudget) ([]scanTask, error) {
	children := []scanTask{}
	add := func(name string, index int, reader io.Reader, isDir, isLink bool) error {
		budget.entries++
		if budget.entries > budget.p.MaxEntries {
			return errors.New("resource_limit_exceeded: archive entry count")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := archiveName(name); err != nil {
			return err
		}
		if isLink {
			return errors.New("unsupported_archive_link: safe virtual link resolution is not implemented")
		}
		if isDir {
			return nil
		}
		targetKind := mediaType(name)
		// Drain RAR entries too: otherwise Next could decompress solid data outside our byte accounting.
		if targetKind == "" {
			_, err := budget.copyEntry(ctx, reader, io.Discard)
			return err
		}
		f, err := os.CreateTemp(workDir, "entry-*"+strings.ToLower(filepath.Ext(name)))
		if err != nil {
			return err
		}
		_, copyErr := budget.copyEntry(ctx, reader, f)
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		loc := Location{RelativePath: parent.Location.RelativePath, ArchiveEntries: append(append([]ArchiveEntry(nil), parent.Location.ArchiveEntries...), ArchiveEntry{Name: name, Index: index})}
		children = append(children, scanTask{Path: f.Name(), Location: loc, Archives: parent.Archives})
		return nil
	}
	if kind == "zip" {
		z, err := zip.OpenReader(parent.Path)
		if err != nil {
			return nil, err
		}
		defer func() { _ = z.Close() }()
		if len(z.File) > budget.p.MaxEntries-budget.entries {
			return nil, errors.New("resource_limit_exceeded: ZIP entry count")
		}
		for i, entry := range z.File {
			if entry.UncompressedSize64 > uint64(budget.p.MaxEntryBytes) {
				return nil, errors.New("resource_limit_exceeded: ZIP entry size")
			}
			if entry.Flags&1 != 0 {
				return nil, errors.New("encrypted_archive_entry")
			}
			r, err := entry.Open()
			if err != nil {
				return nil, err
			}
			err = add(entry.Name, i, r, entry.FileInfo().IsDir(), entry.Mode()&os.ModeSymlink != 0)
			closeErr := r.Close()
			if err != nil {
				return nil, err
			}
			if closeErr != nil {
				return nil, closeErr
			}
		}
	} else {
		f, err := os.Open(parent.Path)
		if err != nil {
			return nil, err
		}
		defer func() { _ = f.Close() }()
		reader, err := rardecode.NewReader(contextReader{ctx, f}, rardecode.MaxDictionarySize(64<<20))
		if err != nil {
			return nil, err
		}
		for i := 0; ; i++ {
			header, err := reader.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, err
			}
			if header.Encrypted || header.HeaderEncrypted {
				return nil, errors.New("encrypted_archive_entry")
			}
			if header.UnPackedSize > budget.p.MaxEntryBytes {
				return nil, errors.New("resource_limit_exceeded: RAR entry size")
			}
			if err := add(header.Name, i, reader, header.IsDir, header.LinkType != 0 || header.Mode()&os.ModeSymlink != 0); err != nil {
				return nil, err
			}
		}
	}
	return children, nil
}

// materialize retrieves only the requested logical item; preview routes never call it for archives.
func materialize(ctx context.Context, source Source, loc Location, dir string, p ProcessingSettings) (string, error) {
	root, err := realPath(source.RootPath)
	if err != nil {
		return "", err
	}
	current, err := sourcePath(root, loc.RelativePath)
	if err != nil {
		return "", err
	}
	if !inside(root, current) {
		return "", errors.New("path outside source")
	}
	budget := &scanBudget{p: p}
	for depth, target := range loc.ArchiveEntries {
		if _, err := archiveName(target.Name); err != nil {
			return "", err
		}
		child, err := extractTarget(ctx, current, target, dir, budget)
		if err != nil {
			return "", fmt.Errorf("archive level %d: %w", depth, err)
		}
		current = child
	}
	return current, nil
}
func extractTarget(ctx context.Context, archive string, target ArchiveEntry, dir string, budget *scanBudget) (string, error) {
	var result string
	save := func(name string, r io.Reader) error {
		if name != target.Name {
			return errors.New("archive entry identity changed")
		}
		f, err := os.CreateTemp(dir, "download-*"+filepath.Ext(name))
		if err != nil {
			return err
		}
		_, copyErr := budget.copyEntry(ctx, r, f)
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		result = f.Name()
		return nil
	}
	switch mediaType(archive) {
	case "zip":
		z, err := zip.OpenReader(archive)
		if err != nil {
			return "", err
		}
		defer func() { _ = z.Close() }()
		if target.Index < 0 || target.Index >= len(z.File) {
			return "", errors.New("archive entry missing")
		}
		entry := z.File[target.Index]
		if entry.Mode()&os.ModeSymlink != 0 || entry.FileInfo().IsDir() {
			return "", errors.New("archive entry is not a regular file")
		}
		r, err := entry.Open()
		if err != nil {
			return "", err
		}
		defer func() { _ = r.Close() }()
		if err := save(entry.Name, r); err != nil {
			return "", err
		}
	case "rar":
		f, err := os.Open(archive)
		if err != nil {
			return "", err
		}
		defer func() { _ = f.Close() }()
		r, err := rardecode.NewReader(contextReader{ctx, f}, rardecode.MaxDictionarySize(64<<20))
		if err != nil {
			return "", err
		}
		for i := 0; i <= target.Index; i++ {
			if i >= budget.p.MaxEntries {
				return "", errors.New("resource_limit_exceeded: entry count")
			}
			header, err := r.Next()
			if err != nil {
				return "", err
			}
			if header.Encrypted || header.HeaderEncrypted || header.LinkType != 0 || header.Mode()&os.ModeSymlink != 0 {
				return "", errors.New("unsupported archive entry")
			}
			if i == target.Index {
				if err := save(header.Name, r); err != nil {
					return "", err
				}
			} else {
				if _, err := budget.copyEntry(ctx, r, io.Discard); err != nil {
					return "", err
				}
			}
		}
	default:
		return "", errors.New("unsupported archive")
	}
	if result == "" {
		return "", errors.New("archive entry missing")
	}
	return result, nil
}
