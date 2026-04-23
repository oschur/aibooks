package chunking

import (
	"aibooks/internal/db"
	"fmt"
	"regexp"
	"strings"
)

type Token struct {
	PageNumber int
	Value      string
}

type Chunk struct {
	PageStart  int
	PageEnd    int
	ChunkIndex int
	Text       string
	TokenCount int
}

type Chunker struct {
	ChunkSizeTokens int
	OverlapTokens   int
}

func NewChunker(chunkSizeTokens, overlapTokens int) (*Chunker, error) {
	if chunkSizeTokens <= 0 {
		return nil, fmt.Errorf("chunkSizeTokens must be > 0")
	}
	if overlapTokens < 0 {
		return nil, fmt.Errorf("overlapTokens must be >= 0")
	}
	if overlapTokens >= chunkSizeTokens {
		overlapTokens = chunkSizeTokens - 1
	}
	return &Chunker{
		ChunkSizeTokens: chunkSizeTokens,
		OverlapTokens:   overlapTokens,
	}, nil
}

var whitespaceSplit = regexp.MustCompile(`\s+`)

func tokenizePages(pages []db.OCRPage) []Token {
	var out []Token
	for _, p := range pages {
		normalized := strings.TrimSpace(whitespaceSplit.ReplaceAllString(p.Text, " "))
		if normalized == "" {
			continue
		}
		for _, tok := range strings.Split(normalized, " ") {
			if tok == "" {
				continue
			}
			out = append(out, Token{PageNumber: p.PageNumber, Value: tok})
		}
	}
	return out
}

func (c *Chunker) Chunk(pages []db.OCRPage) ([]Chunk, error) {
	tokens := tokenizePages(pages)
	if len(tokens) == 0 {
		return nil, fmt.Errorf("no OCR tokens to chunk")
	}

	var chunks []Chunk
	i := 0
	chunkIndex := 0
	for i < len(tokens) {
		j := i + c.ChunkSizeTokens
		if j > len(tokens) {
			j = len(tokens)
		}

		startPage := tokens[i].PageNumber
		endPage := tokens[j-1].PageNumber

		text := make([]string, 0, j-i)
		for k := i; k < j; k++ {
			text = append(text, tokens[k].Value)
		}

		chunks = append(chunks, Chunk{
			PageStart:  startPage,
			PageEnd:    endPage,
			ChunkIndex: chunkIndex,
			Text:       strings.Join(text, " "),
			TokenCount: j - i,
		})
		chunkIndex++

		if j == len(tokens) {
			break
		}

		next := j - c.OverlapTokens

		if next <= i {
			next = i + 1
		}
		i = next
	}

	return chunks, nil
}
