package consumer

import (
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"math/bits"
	"os"
	"strconv"
)

// CalculateDHash computes a 64-bit difference hash (dHash) for an image.
// It downscales the image to a 9x8 grid, converts each pixel to grayscale luminance,
// and compares adjacent horizontal pixels. Returns a 16-hex-character string.
func CalculateDHash(img image.Image) uint64 {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w == 0 || h == 0 {
		return 0
	}

	// 9 columns, 8 rows grid
	const cols = 9
	const rows = 8
	var grayGrid [rows][cols]uint32

	for r := 0; r < rows; r++ {
		srcY := bounds.Min.Y + (r*h)/rows
		for c := 0; c < cols; c++ {
			srcX := bounds.Min.X + (c*w)/cols
			rgba := img.At(srcX, srcY)
			red, green, blue, _ := rgba.RGBA()
			// Luminance formula (ITU-R BT.601)
			gray := (299*red + 587*green + 114*blue) / 1000
			grayGrid[r][c] = gray
		}
	}

	var hash uint64
	bitPos := 0
	for r := 0; r < rows; r++ {
		for c := 0; c < cols-1; c++ {
			if grayGrid[r][c] > grayGrid[r][c+1] {
				hash |= (1 << bitPos)
			}
			bitPos++
		}
	}
	return hash
}

// CalculateImageDHashFromFile opens an image file (PNG/JPEG) and calculates its dHash hex string.
func CalculateImageDHashFromFile(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		return "", err
	}

	hashVal := CalculateDHash(img)
	return fmt.Sprintf("%016x", hashVal), nil
}

// HammingDistance calculates the bit difference between two 16-char hex pHashes.
// A distance <= 5 indicates visual duplicate / near-identical document scan.
func HammingDistance(hashHexA, hashHexB string) (int, error) {
	if len(hashHexA) != 16 || len(hashHexB) != 16 {
		return 64, fmt.Errorf("invalid hash length (expected 16 hex characters)")
	}
	valA, err := strconv.ParseUint(hashHexA, 16, 64)
	if err != nil {
		return 64, err
	}
	valB, err := strconv.ParseUint(hashHexB, 16, 64)
	if err != nil {
		return 64, err
	}
	return bits.OnesCount64(valA ^ valB), nil
}

// IsVisualDuplicate checks if two hashes match within the given tolerance (default 5).
func IsVisualDuplicate(hashHexA, hashHexB string, maxDistance int) bool {
	if hashHexA == "" || hashHexB == "" {
		return false
	}
	dist, err := HammingDistance(hashHexA, hashHexB)
	if err != nil {
		return false
	}
	return dist <= maxDistance
}

