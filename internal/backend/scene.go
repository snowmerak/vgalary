package backend

import (
	"context"
	"fmt"
	"image"
	"os"

	"golang.org/x/image/draw"
)

const frameSignatureSize = 64

type frameSignature [frameSignatureSize * frameSignatureSize * 3]uint8

// Compare low-resolution color pixels before embedding, keeping the first frame
// and measuring subsequent candidates against the last accepted frame. Updating
// the reference only on acceptance lets gradual changes accumulate.
func selectChangedFrames(ctx context.Context, refs []PointRef, files []string, threshold float64) ([]PointRef, []string, error) {
	if len(refs) != len(files) {
		return nil, nil, fmt.Errorf("frame selection metadata/output mismatch")
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if threshold == 0 || len(refs) == 0 {
		return refs, files, nil
	}
	selectedRefs := make([]PointRef, 0, len(refs))
	selectedFiles := make([]string, 0, len(files))
	var previous frameSignature
	for i, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		signature, err := readFrameSignature(file)
		if err != nil {
			return nil, nil, fmt.Errorf("frame comparison: %w", err)
		}
		ref := refs[i]
		if i > 0 {
			score := frameChange(previous, signature)
			if score < threshold {
				continue
			}
			ref.ChangeScore = &score
		}
		selectedRefs = append(selectedRefs, ref)
		selectedFiles = append(selectedFiles, file)
		previous = signature
	}
	return selectedRefs, selectedFiles, nil
}

func readFrameSignature(name string) (frameSignature, error) {
	var result frameSignature
	file, err := os.Open(name)
	if err != nil {
		return result, err
	}
	defer func() { _ = file.Close() }()
	// Inputs are already bounded JPEG outputs from our FFmpeg extraction step.
	frame, _, err := image.Decode(file)
	if err != nil {
		return result, err
	}
	thumbnail := image.NewRGBA(image.Rect(0, 0, frameSignatureSize, frameSignatureSize))
	draw.ApproxBiLinear.Scale(thumbnail, thumbnail.Bounds(), frame, frame.Bounds(), draw.Src, nil)
	for y := 0; y < frameSignatureSize; y++ {
		for x := 0; x < frameSignatureSize; x++ {
			pixel := thumbnail.RGBAAt(x, y)
			offset := (y*frameSignatureSize + x) * 3
			result[offset] = pixel.R
			result[offset+1] = pixel.G
			result[offset+2] = pixel.B
		}
	}
	return result, nil
}

// Normalized RGB mean absolute difference: 0 is identical; 1 is maximal change.
func frameChange(a, b frameSignature) float64 {
	total := 0
	for i, value := range a {
		difference := int(value) - int(b[i])
		if difference < 0 {
			difference = -difference
		}
		total += difference
	}
	return float64(total) / (255 * float64(len(a)))
}
