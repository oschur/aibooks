package ocr

import "testing"

func TestCleanText(t *testing.T) {
	in := "hello\u00a0world\r\n\r\n\r\n  foo\t\tbar   "
	got := cleanText(in)
	// Expected:
	// - NBSP -> space
	// - \r -> \n
	// - collapse 3+ newlines into 2
	// - collapse multiple spaces/tabs into one
	// - trim
	want := "hello world\n\nfoo bar"
	if got != want {
		t.Fatalf("cleanText mismatch:\nwant: %q\ngot:  %q", want, got)
	}
}
