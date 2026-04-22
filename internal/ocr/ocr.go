package ocr

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type Engine struct {
	lang string
	dpi  int
}

func NewEngine(lang string, dpi int) *Engine {
	return &Engine{lang: lang, dpi: dpi}
}

func (e *Engine) ConvertPDFToPNGs(pdfPath, outDir string) ([]string, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}

	prefix := filepath.Join(outDir, "page")

	cmd := exec.Command("pdftoppm",
		"-png",
		"-r", strconv.Itoa(e.dpi),
		pdfPath,
		prefix,
	)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("pdftoppm failed: %w", err)
	}

	paths, err := filepath.Glob(prefix + "-*.png")
	if err != nil {
		return nil, err
	}

	sort.Slice(paths, func(i, j int) bool {
		return pageIndexFromPath(paths[i]) < pageIndexFromPath(paths[j])
	})

	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if pageIndexFromPath(p) > 0 {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no OCR pages generated from %s", pdfPath)
	}
	return out, nil
}

// получаем индекс страницы из пути
func pageIndexFromPath(p string) int {
	base := filepath.Base(p)

	re := regexp.MustCompile(`-(\d+)\.png$`)
	m := re.FindStringSubmatch(base)
	if len(m) != 2 {
		return 0
	}
	v, _ := strconv.Atoi(m[1])
	return v
}

// чистим текст от мусора
func cleanText(in string) string {
	in = strings.ReplaceAll(in, "\u00a0", " ")
	in = strings.ReplaceAll(in, "\r", "\n")
	in = regexp.MustCompile(`\n{3,}`).ReplaceAllString(in, "\n\n")
	in = regexp.MustCompile(`[ \t]{2,}`).ReplaceAllString(in, " ")
	in = regexp.MustCompile(`\n[ \t]+`).ReplaceAllString(in, "\n")
	in = strings.TrimSpace(in)
	return in
}

// выполняем OCR изображения
func (e *Engine) OCRImageText(imagePath string) (string, error) {
	cmd := exec.Command("tesseract", imagePath, "stdout", "-l", e.lang)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("tesseract failed: %w: %s", err, stderr.String())
	}
	return cleanText(string(out)), nil
}
