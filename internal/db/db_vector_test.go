package db

import (
	"strings"
	"testing"
)

func TestVectorToPGString(t *testing.T) {
	in := []float32{0.1, 0.2, -1.5}
	out := vectorToPGString(in)
	if !strings.HasPrefix(out, "[") || !strings.HasSuffix(out, "]") {
		t.Fatalf("expected bracketed vector, got %q", out)
	}
	if !strings.Contains(out, "0.1") || !strings.Contains(out, "0.2") || !strings.Contains(out, "-1.5") {
		t.Fatalf("unexpected vector string: %q", out)
	}
}
