package upload

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"time"
)

func SaveUploadedFileAndChecksum(file multipart.File, dir string) (string, string, error) {
	h := sha256.New()

	tmpName := fmt.Sprintf("upload-%d", time.Now().UnixNano())
	tmpPath := filepath.Join(dir, tmpName)

	out, err := os.Create(tmpPath)
	if err != nil {
		return "", "", err
	}
	defer out.Close()

	w := io.MultiWriter(out, h)
	if _, err := io.Copy(w, file); err != nil {
		return "", "", err
	}
	if err := out.Sync(); err != nil {
		return "", "", err
	}

	checksum := hex.EncodeToString(h.Sum(nil))
	return tmpPath, checksum, nil
}
