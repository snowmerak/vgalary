package backend

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSettingsEnvironment(t *testing.T) {
	s := DefaultSettings()
	if _, err := resolve(s); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_VGALARY_URL", "http://127.0.0.1:9000/v1/")
	t.Setenv("TEST_VGALARY_KEY", "test-secret")
	s.Embedding.BaseURLEnv = "TEST_VGALARY_URL"
	s.Embedding.APIKeyEnv = "TEST_VGALARY_KEY"
	resolved, err := resolve(s)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.EmbeddingURL != "http://127.0.0.1:9000/v1" || resolved.EmbeddingKey != "test-secret" {
		t.Fatal("environment override not applied")
	}
	raw, _ := json.Marshal(s)
	if strings.Contains(string(raw), "test-secret") {
		t.Fatal("secret persisted")
	}
	s.Embedding.ModelEnv = "TEST_VGALARY_MISSING"
	t.Setenv("TEST_VGALARY_MISSING", "")
	if _, err := resolve(s); err == nil {
		t.Fatal("missing configured variable silently fell back")
	}
}

func TestArchiveNames(t *testing.T) {
	for _, name := range []string{"../a.jpg", "a/../../a.jpg", "/a.jpg", `C:\a.jpg`, `..\a.jpg`, "a\x00b.jpg"} {
		if _, err := archiveName(name); err == nil {
			t.Errorf("accepted %q", name)
		}
	}
	for _, name := range []string{"한글/사진.jpg", `a\b.jpg`, "a/b/c.jpg"} {
		if _, err := archiveName(name); err != nil {
			t.Errorf("rejected %q: %v", name, err)
		}
	}
}

func TestNALIDRDetection(t *testing.T) {
	cases := []struct {
		name, codec string
		packet      []byte
		length      int
		want        bool
		invalid     bool
	}{
		{"h264 idr annexb", "h264", []byte{0, 0, 0, 1, 0x65, 1, 2}, 0, true, false},
		{"h264 non-idr I NAL", "h264", []byte{0, 0, 1, 0x41, 1, 2}, 0, false, false},
		{"h264 avcc", "h264", []byte{0, 0, 0, 2, 0x65, 0}, 4, true, false},
		{"short avcc", "h264", []byte{2, 0x65, 0}, 1, true, false},
		{"hevc idr19", "hevc", []byte{0, 0, 0, 1, 19 << 1, 1}, 0, true, false},
		{"hevc idr20", "hevc", []byte{0, 2, 20 << 1, 1}, 2, true, false},
		{"hevc cra21", "hevc", []byte{0, 0, 1, 21 << 1, 1}, 0, false, false},
		{"bad length", "h264", []byte{0, 0, 0, 9, 0x65}, 4, false, true},
		{"unknown framing", "h264", []byte{0x65, 1, 2}, 0, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := packetHasIDR(tc.packet, tc.codec, tc.length)
			if (err != nil) != tc.invalid || got != tc.want {
				t.Fatalf("got %v,%v; want %v, invalid=%v", got, err, tc.want, tc.invalid)
			}
		})
	}
	dump := "\n00000000: 0000 0002 6500                           ....e.\n"
	b, err := parseHexDump(dump)
	if err != nil || !bytes.Equal(b, []byte{0, 0, 0, 2, 0x65, 0}) {
		t.Fatalf("bad packet dump decode: %x %v", b, err)
	}
}

func makeZIP(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	f, err := w.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func TestDeepNestedArchivesAndSiblingDuplicates(t *testing.T) {
	root := t.TempDir()
	work := t.TempDir()
	data := []byte("image fixture")
	nested := makeZIP(t, "photos/한글.jpg", data)
	for i := 0; i < 40; i++ {
		nested = makeZIP(t, "nested/inner.zip", nested)
	}
	for _, name := range []string{"first.zip", "second.zip"} {
		if err := os.WriteFile(filepath.Join(root, name), nested, 0600); err != nil {
			t.Fatal(err)
		}
	}
	files := []discoveredFile{}
	report := walkSource(context.Background(), Source{RootPath: root}, work, DefaultSettings().Processing, func(file discoveredFile) error { files = append(files, file); return nil })
	if !report.Complete || report.Cycles != 0 || len(files) != 2 {
		t.Fatalf("incomplete traversal: %+v, files=%d", report, len(files))
	}
	for _, f := range files {
		if len(f.Location.ArchiveEntries) != 41 {
			t.Fatalf("chain truncated at %d", len(f.Location.ArchiveEntries))
		}
		actual, err := os.ReadFile(f.Path)
		if err != nil || !bytes.Equal(actual, data) {
			t.Fatal("nested bytes changed")
		}
	}
	materialized, err := materialize(context.Background(), Source{RootPath: root}, files[0].Location, t.TempDir(), DefaultSettings().Processing)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(materialized)
	if err != nil || !bytes.Equal(actual, data) {
		t.Fatal("nested download differs")
	}
}

func createDirectoryLink(t *testing.T, name, target string) {
	t.Helper()
	if err := os.Symlink(target, name); err == nil {
		return
	}
	if runtime.GOOS == "windows" {
		cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", `New-Item -ItemType Junction -Path $env:VGALARY_TEST_LINK -Target $env:VGALARY_TEST_TARGET -ErrorAction Stop | Out-Null`)
		cmd.Env = append(os.Environ(), "VGALARY_TEST_LINK="+name, "VGALARY_TEST_TARGET="+target)
		if err := cmd.Run(); err == nil {
			return
		}
	}
	t.Skip("directory links unavailable")
}
func TestDirectoryCyclesPreserveSiblingAliases(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.MkdirAll(real, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "photo.jpg"), []byte("image"), 0600); err != nil {
		t.Fatal(err)
	}
	createDirectoryLink(t, filepath.Join(real, "back"), root)
	createDirectoryLink(t, filepath.Join(root, "alias"), real)
	visited := 0
	report := walkSource(context.Background(), Source{RootPath: root}, t.TempDir(), DefaultSettings().Processing, func(_ discoveredFile) error { visited++; return nil })
	if !report.Complete || report.Cycles != 2 || visited != 2 {
		t.Fatalf("cycle/alias detection: %+v visited=%d", report, visited)
	}
}
func TestArchiveLimitsAndTraversal(t *testing.T) {
	for _, tc := range []struct {
		name  string
		data  []byte
		limit int64
	}{{"traversal", makeZIP(t, "../escape.jpg", []byte("bad")), 1000}, {"expansion", makeZIP(t, "a.jpg", bytes.Repeat([]byte{1}, 2000)), 1000}} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "bad.zip"), tc.data, 0600); err != nil {
				t.Fatal(err)
			}
			p := DefaultSettings().Processing
			p.MaxEntryBytes = tc.limit
			p.MaxExpandedBytes = tc.limit
			report := walkSource(context.Background(), Source{RootPath: root}, t.TempDir(), p, func(_ discoveredFile) error { t.Fatal("invalid archive reached visitor"); return nil })
			if report.Complete || len(report.Issues) == 0 {
				t.Fatalf("invalid archive silently accepted: %+v", report)
			}
		})
	}
}
func TestSettingsPersistenceAndOriginProtection(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	app, err := New(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	settings := app.Settings()
	settings.Embedding.Model = "alternate-model"
	if err := app.SaveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	source, err := app.AddSource(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	invalid := settings
	invalid.Embedding.APIKeyEnv = "not an env var"
	if err := app.SaveSettings(ctx, invalid); err == nil {
		t.Fatal("accepted invalid configuration")
	}
	if app.Settings().Embedding.Model != "alternate-model" {
		t.Fatal("rejected update changed active settings")
	}
	badOrigin := httptest.NewRequest(http.MethodPut, "http://localhost/api/settings", strings.NewReader(`{}`))
	badOrigin.Header.Set("Origin", "https://untrusted.example")
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, badOrigin)
	if response.Code != 403 {
		t.Fatalf("cross-origin mutation: %d", response.Code)
	}
	badHost := httptest.NewRequest(http.MethodGet, "http://untrusted.example/api/settings", nil)
	response = httptest.NewRecorder()
	app.Handler().ServeHTTP(response, badHost)
	if response.Code != 403 {
		t.Fatal("untrusted Host accepted")
	}
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	if reopened.Settings().Embedding.Model != "alternate-model" {
		t.Fatal("settings lost on restart")
	}
	sources, err := reopened.Sources(ctx)
	if err != nil || len(sources) != 1 || sources[0].ID != source.ID {
		t.Fatal("sources lost on restart")
	}
	if err := reopened.RemoveSource(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(source.RootPath); err != nil {
		t.Fatal("source removal touched original directory")
	}
}

func TestFFmpegIDRTimestamps(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	p := DefaultSettings().Processing
	p.VideoFrameChangeThreshold = 0 // This test isolates IDR/PTS extraction from scene selection.
	for _, codec := range []string{"libx264", "libx265"} {
		t.Run(codec, func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(dir, "sample.mp4")
			args := []string{"-nostdin", "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=128x96:rate=10", "-t", "2", "-c:v", codec, "-g", "10", "-bf", "2"}
			if codec == "libx264" {
				args = append(args, "-x264-params", "scenecut=0:open-gop=0")
			} else {
				args = append(args, "-x265-params", "scenecut=0:open-gop=0:log-level=error")
			}
			args = append(args, file)
			if _, err := runMedia(ctx, p, p.FFmpeg, args...); err != nil {
				t.Fatal(err)
			}
			refs, files, err := videoFrames(ctx, file, filepath.Join(dir, "frames"), p)
			if err != nil {
				t.Fatal(err)
			}
			if len(refs) != 2 || len(files) != 2 {
				t.Fatalf("expected two IDRs; got %d", len(refs))
			}
			for i, ref := range refs {
				if ref.SelectionKind != "idr" || ref.TimestampMS == nil || *ref.TimestampMS != int64(i*1000) {
					t.Fatalf("incorrect IDR timestamp %+v", ref)
				}
			}
		})
	}
}
