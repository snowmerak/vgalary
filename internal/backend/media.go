package backend

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// cappedBuffer bounds diagnostic/probe output while continuing to drain the child.
type cappedBuffer struct {
	bytes.Buffer
	limit    int
	overflow bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - b.Len()
	if len(p) > remaining {
		b.overflow = true
		p = p[:max(0, remaining)]
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}

func runMedia(ctx context.Context, p ProcessingSettings, exe string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(p.ProcessTimeoutSeconds)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	out := &cappedBuffer{limit: 16 << 20}
	diagnostic := &cappedBuffer{limit: 4096}
	cmd.Stdout = out
	cmd.Stderr = diagnostic
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("media process failed (%s)", filepath.Base(exe))
	}
	if out.overflow {
		return nil, errors.New("media probe output exceeded limit")
	}
	return out.Bytes(), nil
}

func imageJPEG(path string, p ProcessingSettings) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return nil, fmt.Errorf("unsupported image: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > p.MaxImagePixels {
		return nil, errors.New("image pixel limit exceeded")
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, err
	}
	width, height := img.Bounds().Dx(), img.Bounds().Dy()
	largest := max(width, height)
	if largest > p.ImageMaxDimension {
		w := max(1, width*p.ImageMaxDimension/largest)
		h := max(1, height*p.ImageMaxDimension/largest)
		resized := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.CatmullRom.Scale(resized, resized.Bounds(), img, img.Bounds(), draw.Over, nil)
		img = resized
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

type videoStream struct {
	Index         int    `json:"index"`
	Codec         string `json:"codec_name"`
	TimeBase      string `json:"time_base"`
	StartTime     string `json:"start_time"`
	NALLengthSize string `json:"nal_length_size"`
	Extradata     string `json:"extradata"`
	Disposition   struct {
		AttachedPic int `json:"attached_pic"`
	} `json:"disposition"`
}
type probeFrame struct {
	PTS     json.Number `json:"pts"`
	BestPTS json.Number `json:"best_effort_timestamp"`
	Time    string      `json:"best_effort_timestamp_time"`
	Key     int         `json:"key_frame"`
}

func videoFrames(ctx context.Context, path, dir string, p ProcessingSettings) ([]PointRef, []string, error) {
	var err error
	var metadata struct {
		Streams []videoStream `json:"streams"`
	}
	// Probe again using V:0: FFmpeg excludes attached cover pictures with uppercase V.
	b, err := runMedia(ctx, p, p.FFprobe, "-v", "error", "-select_streams", "V:0", "-show_data", "-show_entries", "stream=index,codec_name,time_base,start_time,nal_length_size,extradata", "-of", "json", path)
	if err != nil {
		return nil, nil, err
	}
	if err := json.Unmarshal(b, &metadata); err != nil {
		return nil, nil, err
	}
	if len(metadata.Streams) != 1 {
		return nil, nil, errors.New("no usable video stream")
	}
	stream := metadata.Streams[0]
	isNAL := stream.Codec == "h264" || stream.Codec == "hevc"
	if !isNAL && !p.AllowKeyframeFallback {
		return nil, nil, fmt.Errorf("idr_detection_unavailable: %s; enable allow_keyframe_fallback for this codec", stream.Codec)
	}
	b, err = runMedia(ctx, p, p.FFprobe, "-v", "error", "-select_streams", "V:0", "-skip_frame", "nokey", "-show_frames", "-show_entries", "frame=pts,best_effort_timestamp,best_effort_timestamp_time,key_frame", "-of", "json", path)
	if err != nil {
		return nil, nil, err
	}
	var frames struct {
		Frames []probeFrame `json:"frames"`
	}
	if err := json.Unmarshal(b, &frames); err != nil {
		return nil, nil, err
	}
	if len(frames.Frames) == 0 {
		return nil, nil, errors.New("no_keyframes_found")
	}
	if len(frames.Frames) > p.MaxEntries {
		return nil, nil, errors.New("resource_limit_exceeded: video keyframe count")
	}
	var idrs map[string]bool
	if isNAL {
		nalLengthSize, _ := strconv.Atoi(stream.NALLengthSize)
		extra, parseErr := parseHexDump(stream.Extradata)
		if parseErr != nil {
			return nil, nil, parseErr
		}
		if nalLengthSize == 0 && stream.Codec == "hevc" && len(extra) >= 22 && extra[0] == 1 {
			nalLengthSize = int(extra[21]&3) + 1
		}
		if nalLengthSize == 0 && stream.Codec == "h264" && len(extra) >= 5 && extra[0] == 1 {
			nalLengthSize = int(extra[4]&3) + 1
		}
		idrs, err = idrPackets(ctx, path, stream.Codec, nalLengthSize, p)
		if err != nil {
			return nil, nil, err
		}
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, nil, err
	}
	scale := fmt.Sprintf("scale=w='min(%d,iw)':h='min(%d,ih)':force_original_aspect_ratio=decrease", p.ImageMaxDimension, p.ImageMaxDimension)
	_, err = runMedia(ctx, p, p.FFmpeg, "-nostdin", "-v", "error", "-skip_frame", "nokey", "-i", path, "-map", fmt.Sprintf("0:%d", stream.Index), "-vf", scale, "-fps_mode", "passthrough", "-frames:v", strconv.Itoa(p.MaxEntries+1), "-q:v", "3", filepath.Join(dir, "%09d.jpg"))
	if err != nil {
		return nil, nil, err
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.jpg"))
	if err != nil {
		return nil, nil, err
	}
	if len(files) != len(frames.Frames) {
		return nil, nil, fmt.Errorf("frame metadata/output mismatch: %d/%d", len(frames.Frames), len(files))
	}
	start := 0.0
	if stream.StartTime != "" {
		start, err = strconv.ParseFloat(stream.StartTime, 64)
		if err != nil {
			return nil, nil, errors.New("invalid stream start time")
		}
	}
	refs := []PointRef{}
	selected := []string{}
	seen := map[string]bool{}
	for i, frame := range frames.Frames {
		pts := frame.PTS.String()
		if pts == "" {
			pts = frame.BestPTS.String()
		}
		if frame.Key != 1 || pts == "" {
			return nil, nil, errors.New("keyframe timestamp unavailable")
		}
		if isNAL && !idrs[pts] {
			continue
		}
		if seen[pts] {
			return nil, nil, errors.New("duplicate keyframe PTS")
		}
		seen[pts] = true
		seconds, err := strconv.ParseFloat(frame.Time, 64)
		if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
			return nil, nil, errors.New("invalid keyframe presentation time")
		}
		ms := int64(math.Round((seconds - start) * 1000))
		if ms < 0 {
			return nil, nil, errors.New("negative normalized keyframe timestamp")
		}
		kind := "idr"
		if !isNAL {
			kind = "random_access_keyframe"
		}
		refs = append(refs, PointRef{TimestampMS: &ms, PTS: pts, TimeBase: stream.TimeBase, SelectionKind: kind, StreamIndex: stream.Index})
		selected = append(selected, files[i])
	}
	if len(refs) == 0 {
		return nil, nil, errors.New("no_idr_found")
	}
	return selectChangedFrames(ctx, refs, selected, p.VideoFrameChangeThreshold)
}

// idrPackets parses packet bytes, never equating the container key flag with IDR.
// ffprobe is streamed so an entire video's hex dump is not retained in memory.
func idrPackets(ctx context.Context, path, codec string, nalLengthSize int, p ProcessingSettings) (map[string]bool, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(p.ProcessTimeoutSeconds)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, p.FFprobe, "-v", "error", "-select_streams", "V:0", "-show_packets", "-show_data", "-show_entries", "packet=pts,flags,data", "-of", "json", path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = &cappedBuffer{limit: 4096}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	result := map[string]bool{}
	parseErr := func() error {
		dec := json.NewDecoder(stdout)
		if _, err := dec.Token(); err != nil {
			return err
		}
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return err
			}
			if key != "packets" {
				var skip json.RawMessage
				if err := dec.Decode(&skip); err != nil {
					return err
				}
				continue
			}
			if _, err := dec.Token(); err != nil {
				return err
			}
			for dec.More() {
				var packet struct {
					PTS   json.Number `json:"pts"`
					Flags string      `json:"flags"`
					Data  string      `json:"data"`
				}
				if err := dec.Decode(&packet); err != nil {
					return err
				}
				if !strings.Contains(packet.Flags, "K") {
					continue
				}
				raw, err := parseHexDump(packet.Data)
				if err != nil {
					return err
				}
				idr, err := packetHasIDR(raw, codec, nalLengthSize)
				if err != nil {
					return err
				}
				if idr {
					if packet.PTS.String() == "" {
						return errors.New("IDR packet has no PTS")
					}
					result[packet.PTS.String()] = true
				}
			}
			if _, err := dec.Token(); err != nil {
				return err
			}
		}
		_, err := dec.Token()
		return err
	}()
	if parseErr != nil {
		cancel()
	}
	waitErr := cmd.Wait()
	if parseErr != nil {
		return nil, fmt.Errorf("IDR packet parse: %w", parseErr)
	}
	if waitErr != nil {
		return nil, fmt.Errorf("IDR probe failed: %w", waitErr)
	}
	return result, nil
}

func parseHexDump(dump string) ([]byte, error) {
	var result []byte
	for _, line := range strings.Split(dump, "\n") {
		_, right, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		right = strings.TrimLeft(right, " ")
		if i := strings.Index(right, "  "); i >= 0 {
			right = right[:i]
		}
		raw, err := hex.DecodeString(strings.ReplaceAll(strings.TrimSpace(right), " ", ""))
		if err != nil {
			return nil, err
		}
		result = append(result, raw...)
	}
	return result, nil
}
func packetHasIDR(b []byte, codec string, nalLengthSize int) (bool, error) {
	isIDR := func(nal []byte) bool {
		if len(nal) == 0 {
			return false
		}
		if codec == "h264" {
			return nal[0]&31 == 5
		}
		t := (nal[0] >> 1) & 63
		return t == 19 || t == 20
	}
	// Annex B starts with a three- or four-byte start code.
	if nalLengthSize == 0 && len(b) >= 3 && b[0] == 0 && b[1] == 0 && (b[2] == 1 || (len(b) > 3 && b[2] == 0 && b[3] == 1)) {
		for i := 0; i+3 <= len(b); i++ {
			if b[i] == 0 && b[i+1] == 0 {
				j := i + 2
				if b[j] == 0 && j+1 < len(b) {
					j++
				}
				if b[j] == 1 && j+1 < len(b) && isIDR(b[j+1:]) {
					return true, nil
				}
			}
		}
		return false, nil
	}
	// AVCC/HVCC framing comes from stream extradata; never guess a prefix width.
	if nalLengthSize >= 1 && nalLengthSize <= 4 {
		width := nalLengthSize
		pos := 0
		found := false
		valid := true
		for pos < len(b) {
			if pos+width > len(b) {
				valid = false
				break
			}
			n := 0
			for _, v := range b[pos : pos+width] {
				n = (n << 8) | int(v)
			}
			pos += width
			if n <= 0 || n > len(b)-pos {
				valid = false
				break
			}
			found = found || isIDR(b[pos:pos+n])
			pos += n
		}
		if valid && pos == len(b) && pos > 0 {
			return found, nil
		}
	}
	return false, errors.New("idr_detection_unavailable: invalid NAL packet framing")
}
