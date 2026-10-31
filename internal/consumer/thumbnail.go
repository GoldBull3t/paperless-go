package consumer

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func GenerateThumbnail(inputPath, outputPath, mimeType string) error {
	// 1. If it's an image, create a resized copy directly
	if strings.HasPrefix(mimeType, "image/") {
		file, err := os.Open(inputPath)
		if err == nil {
			defer file.Close()
			img, _, err := image.Decode(file)
			if err == nil {
				outFile, err := os.Create(outputPath)
				if err == nil {
					defer outFile.Close()
					return png.Encode(outFile, img)
				}
			}
		}
	}

	// 2. Try pdftoppm (standard poppler utility, blazing fast)
	if _, err := exec.LookPath("pdftoppm"); err == nil {
		prefix := strings.TrimSuffix(outputPath, filepath.Ext(outputPath))
		cmd := exec.Command("pdftoppm", "-png", "-r", "150", "-f", "1", "-l", "1", "-scale-to", "500", inputPath, prefix)
		if err := cmd.Run(); err == nil {
			// pdftoppm appends -1.png or -01.png
			generated := prefix + "-1.png"
			if _, err := os.Stat(generated); err == nil {
				_ = os.Rename(generated, outputPath)
				return nil
			}
		}
	}

	// 3. Try ImageMagick
	if _, err := exec.LookPath("magick"); err == nil {
		cmd := exec.Command("magick", inputPath+"[0]", "-thumbnail", "500x", outputPath)
		if err := cmd.Run(); err == nil {
			return nil
		}
	}

	// 4. Fallback: generate a clean minimalist document preview placeholder
	return createPlaceholderThumbnail(outputPath, filepath.Base(inputPath))
}

func createPlaceholderThumbnail(outputPath, title string) error {
	width := 300
	height := 420
	img := image.NewRGBA(image.Rect(0, 0, width, height))

	// Background: off-white document paper
	paperColor := color.RGBA{R: 248, G: 249, B: 250, A: 255}
	borderColor := color.RGBA{R: 222, G: 226, B: 230, A: 255}
	lineColor := color.RGBA{R: 206, G: 212, B: 218, A: 255}
	accentColor := color.RGBA{R: 25, G: 135, B: 84, A: 255} // Default green

	ext := strings.ToLower(filepath.Ext(title))
	switch ext {
	case ".docx", ".doc", ".odt":
		accentColor = color.RGBA{R: 43, G: 87, B: 154, A: 255} // Word blue
	case ".xlsx", ".xls", ".ods":
		accentColor = color.RGBA{R: 33, G: 115, B: 70, A: 255} // Excel green
	case ".pptx", ".ppt", ".odp":
		accentColor = color.RGBA{R: 210, G: 71, B: 38, A: 255} // PowerPoint orange
	}

	draw.Draw(img, img.Bounds(), &image.Uniform{C: paperColor}, image.Point{}, draw.Src)

	// Draw paper border
	for x := 0; x < width; x++ {
		img.Set(x, 0, borderColor)
		img.Set(x, height-1, borderColor)
	}
	for y := 0; y < height; y++ {
		img.Set(0, y, borderColor)
		img.Set(width-1, y, borderColor)
	}

	// Header banner (accent line)
	for x := 20; x < width-20; x++ {
		for y := 30; y < 36; y++ {
			img.Set(x, y, accentColor)
		}
	}

	// Simulated document lines
	for row := 0; row < 12; row++ {
		yPos := 70 + row*24
		lineWidth := width - 40
		if row%3 == 0 {
			lineWidth = width - 100
		}
		for x := 20; x < lineWidth; x++ {
			for y := yPos; y < yPos+4; y++ {
				img.Set(x, y, lineColor)
			}
		}
	}

	outFile, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer outFile.Close()

	return png.Encode(outFile, img)
}
