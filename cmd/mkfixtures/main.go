// mkfixtures copies explicitly supplied media into ignored local integration fixtures.
package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
)

type paths []string

func (p *paths) String() string         { return fmt.Sprint([]string(*p)) }
func (p *paths) Set(value string) error { *p = append(*p, value); return nil }

type member struct {
	name string
	data []byte
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	var images paths
	flag.Var(&images, "image", "source image path (repeat for multiple images)")
	video := flag.String("video", "", "source video path")
	output := flag.String("out", "testdata/local/media", "new output directory")
	flag.Parse()
	if len(images) < 4 || *video == "" {
		return fmt.Errorf("provide at least four -image paths and one -video path")
	}
	root, err := filepath.Abs(*output)
	if err != nil {
		return err
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		return fmt.Errorf("output must be a new directory: %s", root)
	}
	imageData := make([][]byte, len(images))
	manifest := []map[string]any{}
	for i, name := range images {
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		imageData[i] = data
		sum := sha256.Sum256(data)
		manifest = append(manifest, map[string]any{"source": name, "sha256": hex.EncodeToString(sum[:]), "bytes": len(data)})
	}
	videoData, err := os.ReadFile(*video)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(videoData)
	manifest = append(manifest, map[string]any{"source": *video, "sha256": hex.EncodeToString(sum[:]), "bytes": len(videoData)})
	write := func(name string, data []byte) error {
		target := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0600)
	}
	if err := write("loose/hand-held-toy.jpg", imageData[0]); err != nil {
		return err
	}
	deep := "deep"
	for i := 1; i <= 24; i++ {
		deep = filepath.Join(deep, fmt.Sprintf("level-%02d", i))
	}
	if err := write(filepath.Join(deep, "금색-배지.jpg"), imageData[1]); err != nil {
		return err
	}
	if err := write("video/original.webm", videoData); err != nil {
		return err
	}
	inner, err := zipBytes([]member{{"photos/파란-인형.jpg", imageData[3]}, {"video/nested.webm", videoData}})
	if err != nil {
		return err
	}
	rar, err := rar4Stored([]member{{"photos/towel.jpg", imageData[2]}, {"nested/inner.zip", inner}})
	if err != nil {
		return err
	}
	if err := write("archives/media.rar", rar); err != nil {
		return err
	}
	outer, err := zipBytes([]member{{"nested/media.rar", rar}})
	if err != nil {
		return err
	}
	// Multiple additional wrappers exercise depth independently of ordinary directory depth.
	for i := 0; i < 8; i++ {
		outer, err = zipBytes([]member{{fmt.Sprintf("level-%02d/inside.zip", i), outer}})
		if err != nil {
			return err
		}
	}
	if err := write("archives/깊은-중첩.zip", outer); err != nil {
		return err
	}
	if err := write("duplicates/blue-plush.jpg", imageData[3]); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	// The manifest is outside the scan root so integration tests can inspect it independently.
	if err := os.WriteFile(filepath.Join(filepath.Dir(root), "manifest.json"), encoded, 0600); err != nil {
		return err
	}
	linkErr := os.Symlink(root, filepath.Join(root, "deep", "ancestor-loop"))
	result := map[string]any{"root": root, "directory_depth": 24, "nested_archives": "ZIP × 9 → RAR → ZIP → image/video", "loop_created": linkErr == nil}
	if linkErr != nil {
		result["loop_note"] = "symlink privilege unavailable; unit test can cover this on supported hosts"
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}
func zipBytes(entries []member) ([]byte, error) {
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for _, entry := range entries {
		f, err := w.CreateHeader(&zip.FileHeader{Name: entry.name, Method: zip.Store})
		if err != nil {
			return nil, err
		}
		if _, err := f.Write(entry.data); err != nil {
			return nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// This fixture-only writer emits single-volume RAR4 "store" entries, not a compressor.
// 7-Zip and the production RAR reader both independently verify its header/data CRCs.
func rar4Stored(entries []member) ([]byte, error) {
	var b bytes.Buffer
	b.Write([]byte{'R', 'a', 'r', '!', 0x1a, 0x07, 0x00})
	header := func(kind byte, flags uint16, body []byte) error {
		raw := []byte{kind, byte(flags), byte(flags >> 8), byte(7 + len(body)), byte((7 + len(body)) >> 8)}
		raw = append(raw, body...)
		if err := binary.Write(&b, binary.LittleEndian, uint16(crc32.ChecksumIEEE(raw))); err != nil {
			return err
		}
		_, err := b.Write(raw)
		return err
	}
	if err := header(0x73, 0, make([]byte, 6)); err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if uint64(len(entry.data)) > uint64(^uint32(0)) || len(entry.name) > 65000 {
			return nil, fmt.Errorf("RAR4 fixture entry is too large")
		}
		var body bytes.Buffer
		values := []any{uint32(len(entry.data)), uint32(len(entry.data)), byte(3), crc32.ChecksumIEEE(entry.data), uint32(0), byte(20), byte(0x30), uint16(len(entry.name)), uint32(0x81a4)}
		for _, v := range values {
			if err := binary.Write(&body, binary.LittleEndian, v); err != nil {
				return nil, err
			}
		}
		body.WriteString(entry.name)
		if err := header(0x74, 0x8000, body.Bytes()); err != nil {
			return nil, err
		}
		if _, err := io.Copy(&b, bytes.NewReader(entry.data)); err != nil {
			return nil, err
		}
	}
	if err := header(0x7b, 0, nil); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
