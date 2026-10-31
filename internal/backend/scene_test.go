package backend

import (
	"context"
	"encoding/json"
	"image"
	"image/color"
	stdDraw "image/draw"
	"image/png"
	"math"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"
)

func testSceneFiles(t *testing.T, colors []color.RGBA) ([]PointRef, []string) {
	t.Helper()
	refs := []PointRef{}
	files := []string{}
	dir := t.TempDir()
	for i, c := range colors {
		frame := image.NewRGBA(image.Rect(0, 0, 128, 96))
		stdDraw.Draw(frame, frame.Bounds(), image.NewUniform(c), image.Point{}, stdDraw.Src)
		name := filepath.Join(dir, strconv.Itoa(i)+".png")
		f, err := os.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		encodeErr := png.Encode(f, frame)
		closeErr := f.Close()
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		timestamp := int64(i * 1000)
		refs = append(refs, PointRef{TimestampMS: &timestamp, PTS: strconv.Itoa(i), TimeBase: "1/1", SelectionKind: "idr"})
		files = append(files, name)
	}
	return refs, files
}

func TestSceneFilterAccumulatesChangeAndPreservesTimestamps(t *testing.T) {
	colors := []color.RGBA{}
	for _, v := range []uint8{0, 8, 16, 24, 32, 40, 64} {
		colors = append(colors, color.RGBA{R: v, G: v, B: v, A: 255})
	}
	refs, files := testSceneFiles(t, colors)
	selected, paths, err := selectChangedFrames(context.Background(), refs, files, 0.12)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 3 {
		t.Fatalf("expected first, 32 and 64 gray levels; got %d", len(selected))
	}
	for i, index := range []int{0, 4, 6} {
		if paths[i] != files[index] || selected[i].PTS != refs[index].PTS || *selected[i].TimestampMS != *refs[index].TimestampMS {
			t.Fatal("selected frame lost its own timestamp")
		}
		if i == 0 {
			if selected[i].ChangeScore != nil {
				t.Fatal("first frame has no comparison reference")
			}
		} else if selected[i].ChangeScore == nil || *selected[i].ChangeScore < 0.12 {
			t.Fatal("accepted insignificant frame")
		}
	}
	// Selection does not mutate the caller's full candidate list.
	for _, ref := range refs {
		if ref.ChangeScore != nil {
			t.Fatal("candidate metadata mutated")
		}
	}
}

func TestSceneFilterStaticColorAndDisable(t *testing.T) {
	red := color.RGBA{R: 255, A: 255}
	blue := color.RGBA{B: 255, A: 255}
	refs, files := testSceneFiles(t, []color.RGBA{red, red, blue, blue})
	selected, _, err := selectChangedFrames(context.Background(), refs, files, 0.12)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 2 || selected[0].PTS != "0" || selected[1].PTS != "2" {
		t.Fatalf("static/color change selection: %+v", selected)
	}
	all, paths, err := selectChangedFrames(context.Background(), refs, files, 0)
	if err != nil || !reflect.DeepEqual(all, refs) || !reflect.DeepEqual(paths, files) {
		t.Fatal("zero did not preserve legacy behavior")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := selectChangedFrames(ctx, refs, files, 0.12); err == nil {
		t.Fatal("ignored cancellation")
	}
	if _, _, err := selectChangedFrames(context.Background(), refs, files[:1], 0.12); err == nil {
		t.Fatal("ignored metadata mismatch")
	}
}

func TestFrameChangeRange(t *testing.T) {
	var black, white frameSignature
	for i := range white {
		white[i] = 255
	}
	if frameChange(black, black) != 0 || frameChange(black, white) != 1 || frameChange(white, black) != 1 {
		t.Fatal("difference is not normalized/symmetric")
	}
}

func TestSceneThresholdValidationAndProfile(t *testing.T) {
	s := DefaultSettings()
	base, err := resolve(s)
	if err != nil {
		t.Fatal(err)
	}
	s.Processing.VideoFrameChangeThreshold = 0.2
	changed, _ := resolve(s)
	if base.profileID() == changed.profileID() {
		t.Fatal("threshold change reused stale profile")
	}
	s.Processing.VideoFrameChangeThreshold = 0
	disabled, _ := resolve(s)
	legacy, _ := json.Marshal([]any{"v1", disabled.Model, disabled.Embedding.ModelRevision, disabled.Embedding.QueryPrefix, disabled.Processing.ImageMaxDimension, disabled.Processing.AllowKeyframeFallback})
	if disabled.profileID() != digest(legacy)[:24] {
		t.Fatal("disabled filter invalidated legacy profile")
	}
	for _, threshold := range []float64{-0.1, 1.1, math.NaN(), math.Inf(1)} {
		s.Processing.VideoFrameChangeThreshold = threshold
		if _, err := resolve(s); err == nil {
			t.Errorf("accepted threshold %v", threshold)
		}
	}
}

func TestExistingSettingsAcquireSceneDefault(t *testing.T) {
	for _, explicitZero := range []bool{false, true} {
		t.Run(strconv.FormatBool(explicitZero), func(t *testing.T) {
			dir := t.TempDir()
			store, err := openStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			s := DefaultSettings()
			s.Embedding.Model = "custom-model"
			raw, err := json.Marshal(s)
			if err != nil {
				t.Fatal(err)
			}
			var old map[string]any
			if err := json.Unmarshal(raw, &old); err != nil {
				t.Fatal(err)
			}
			processing := old["processing"].(map[string]any)
			if explicitZero {
				processing["video_frame_change_threshold"] = 0
			} else {
				delete(processing, "video_frame_change_threshold")
			}
			if err := store.put(context.Background(), "settings", "current", old); err != nil {
				t.Fatal(err)
			}
			if err := store.db.Close(); err != nil {
				t.Fatal(err)
			}
			app, err := New(context.Background(), dir)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = app.Close() }()
			expected := DefaultSettings().Processing.VideoFrameChangeThreshold
			if explicitZero {
				expected = 0
			}
			if app.Settings().Processing.VideoFrameChangeThreshold != expected || app.Settings().Embedding.Model != "custom-model" {
				t.Fatal("settings migration lost defaults or explicit values")
			}
		})
	}
}

func TestAssetListDefaultsToActiveFramePolicy(t *testing.T) {
	app, err := New(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Close() }()
	source, err := app.AddSource(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range []string{"old", "new"} {
		asset := Asset{ID: profile, SourceID: source.ID, ProfileID: profile}
		if err := app.store.put(context.Background(), "asset", asset.ID, asset); err != nil {
			t.Fatal(err)
		}
	}
	if err := app.store.put(context.Background(), "meta", "active_profile", "new"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ path, want string }{{"/api/assets", "new"}, {"/api/assets?profile_id=old", "old"}} {
		recorder := httptest.NewRecorder()
		app.Handler().ServeHTTP(recorder, httptest.NewRequest("GET", "http://localhost"+tc.path, nil))
		var assets []Asset
		if err := json.Unmarshal(recorder.Body.Bytes(), &assets); err != nil {
			t.Fatal(err)
		}
		if recorder.Code != 200 || len(assets) != 1 || assets[0].ID != tc.want {
			t.Fatalf("mixed old and new frame policies: %s", recorder.Body.String())
		}
	}
}

func TestFFmpegStaticIDRsAreFiltered(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	p := DefaultSettings().Processing
	dir := t.TempDir()
	input := filepath.Join(dir, "static.mp4")
	if _, err := runMedia(ctx, p, p.FFmpeg, "-nostdin", "-v", "error", "-f", "lavfi", "-i", "color=c=red:size=128x96:rate=10", "-t", "3", "-c:v", "libx264", "-g", "10", "-x264-params", "scenecut=0:open-gop=0", input); err != nil {
		t.Fatal(err)
	}
	allPolicy := p
	allPolicy.VideoFrameChangeThreshold = 0
	all, _, err := videoFrames(ctx, input, filepath.Join(dir, "all"), allPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 raw IDRs, got %d", len(all))
	}
	selected, _, err := videoFrames(ctx, input, filepath.Join(dir, "selected"), p)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 1 || selected[0].SelectionKind != "idr" || *selected[0].TimestampMS != 0 {
		t.Fatalf("static IDRs not filtered: %+v", selected)
	}
}

func TestLiveVideoFrameChanges(t *testing.T) {
	input := os.Getenv("VGALARY_VIDEO_TEST_PATH")
	if input == "" {
		t.Skip("set VGALARY_VIDEO_TEST_PATH for actual video comparison")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	p := DefaultSettings().Processing
	p.AllowKeyframeFallback = true
	allPolicy := p
	allPolicy.VideoFrameChangeThreshold = 0
	dir := t.TempDir()
	all, _, err := videoFrames(ctx, input, filepath.Join(dir, "all"), allPolicy)
	if err != nil {
		t.Fatal(err)
	}
	selected, _, err := videoFrames(ctx, input, filepath.Join(dir, "selected"), p)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) == 0 || len(selected) >= len(all) {
		t.Fatalf("sample frame count was not reduced: %d -> %d", len(all), len(selected))
	}
	candidates := map[string]PointRef{}
	for _, ref := range all {
		candidates[ref.PTS] = ref
	}
	for i, ref := range selected {
		original, ok := candidates[ref.PTS]
		if !ok || *original.TimestampMS != *ref.TimestampMS {
			t.Fatal("frame timestamp changed")
		}
		if i > 0 && (ref.ChangeScore == nil || *ref.ChangeScore < p.VideoFrameChangeThreshold) {
			t.Fatal("frame below threshold retained")
		}
	}
	t.Logf("video candidates=%d selected=%d threshold=%.2f", len(all), len(selected), p.VideoFrameChangeThreshold)
}
