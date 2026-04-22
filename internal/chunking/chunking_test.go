package chunking

import (
	"aibooks/internal/db"
	"testing"
)

func TestChunker_ChunkBoundariesAndOverlap(t *testing.T) {
	ch, err := NewChunker(6, 2)
	if err != nil {
		t.Fatalf("NewChunker: %v", err)
	}

	pages := []db.OCRPage{
		{PageNumber: 1, Text: "t1 t2 t3 t4"},
		{PageNumber: 2, Text: "t5 t6 t7 t8"},
	}

	chunks, err := ch.Chunk(pages)
	if err != nil {
		t.Fatalf("Chunk: %v", err)
	}
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}

	if chunks[0].ChunkIndex != 0 {
		t.Fatalf("chunk[0] ChunkIndex expected 0, got %d", chunks[0].ChunkIndex)
	}
	if chunks[0].PageStart != 1 || chunks[0].PageEnd != 2 {
		t.Fatalf("chunk[0] pages expected 1-2, got %d-%d", chunks[0].PageStart, chunks[0].PageEnd)
	}

	if chunks[1].ChunkIndex != 1 {
		t.Fatalf("chunk[1] ChunkIndex expected 1, got %d", chunks[1].ChunkIndex)
	}
	if chunks[1].PageStart != 2 || chunks[1].PageEnd != 2 {
		t.Fatalf("chunk[1] pages expected 2-2, got %d-%d", chunks[1].PageStart, chunks[1].PageEnd)
	}

	toks0 := splitTokens(chunks[0].Text)
	toks1 := splitTokens(chunks[1].Text)
	if len(toks0) < 2 || len(toks1) < 2 {
		t.Fatalf("unexpected token lengths: toks0=%d toks1=%d", len(toks0), len(toks1))
	}
	if toks0[len(toks0)-2] != toks1[0] || toks0[len(toks0)-1] != toks1[1] {
		t.Fatalf("overlap mismatch: chunk0_tail=%q,%q chunk1_head=%q,%q",
			toks0[len(toks0)-2], toks0[len(toks0)-1], toks1[0], toks1[1],
		)
	}
}

func splitTokens(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ' ' || r == '\n' || r == '\t' || r == '\r' {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
