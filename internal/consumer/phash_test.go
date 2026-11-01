package consumer

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestPerceptualHash_IdenticalAndModifiedImages(t *testing.T) {
	tmpDir := t.TempDir()

	// Create base test image (white canvas with black horizontal bar)
	imgA := image.NewRGBA(image.Rect(0, 0, 200, 200))
	draw.Draw(imgA, imgA.Bounds(), &image.Uniform{color.White}, image.Point{}, draw.Src)
	draw.Draw(imgA, image.Rect(20, 80, 180, 120), &image.Uniform{color.Black}, image.Point{}, draw.Src)

	pathA := filepath.Join(tmpDir, "img_a.png")
	fA, _ := os.Create(pathA)
	_ = png.Encode(fA, imgA)
	_ = fA.Close()

	hashA, err := CalculateImageDHashFromFile(pathA)
	if err != nil {
		t.Fatalf("Failed to calculate dHash for imgA: %v", err)
	}
	if len(hashA) != 16 {
		t.Fatalf("Expected 16 hex chars, got %q", hashA)
	}

	// 1. Identical image copy -> distance must be 0
	distSelf, err := HammingDistance(hashA, hashA)
	if err != nil || distSelf != 0 {
		t.Errorf("Expected distance 0 for self comparison, got %d (err: %v)", distSelf, err)
	}

	// 2. Slightly degraded/noisy image (e.g. simulated scanner artifact)
	imgB := image.NewRGBA(image.Rect(0, 0, 200, 200))
	draw.Draw(imgB, imgB.Bounds(), &image.Uniform{color.White}, image.Point{}, draw.Src)
	draw.Draw(imgB, image.Rect(20, 81, 180, 119), &image.Uniform{color.RGBA{20, 20, 20, 255}}, image.Point{}, draw.Src)

	pathB := filepath.Join(tmpDir, "img_b.png")
	fB, _ := os.Create(pathB)
	_ = png.Encode(fB, imgB)
	_ = fB.Close()

	hashB, err := CalculateImageDHashFromFile(pathB)
	if err != nil {
		t.Fatalf("Failed to calculate dHash for imgB: %v", err)
	}

	if !IsVisualDuplicate(hashA, hashB, 5) {
		dist, _ := HammingDistance(hashA, hashB)
		t.Errorf("Expected imgA and imgB to be visual duplicates, got Hamming distance %d", dist)
	}

	// 3. Completely different image (vertical black bar) -> distance > 5
	imgC := image.NewRGBA(image.Rect(0, 0, 200, 200))
	draw.Draw(imgC, imgC.Bounds(), &image.Uniform{color.White}, image.Point{}, draw.Src)
	draw.Draw(imgC, image.Rect(80, 20, 120, 180), &image.Uniform{color.Black}, image.Point{}, draw.Src)

	pathC := filepath.Join(tmpDir, "img_c.png")
	fC, _ := os.Create(pathC)
	_ = png.Encode(fC, imgC)
	_ = fC.Close()

	hashC, err := CalculateImageDHashFromFile(pathC)
	if err != nil {
		t.Fatalf("Failed to calculate dHash for imgC: %v", err)
	}

	distDiff, _ := HammingDistance(hashA, hashC)
	if distDiff <= 5 {
		t.Errorf("Expected different images to have distance > 5, got %d", distDiff)
	}
	if IsVisualDuplicate(hashA, hashC, 5) {
		t.Errorf("Different images incorrectly identified as visual duplicates")
	}
}

