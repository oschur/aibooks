package util

import "testing"

func TestEstimateTokensFromChars(t *testing.T) {
	if got := EstimateTokensFromChars(0); got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
	if got := EstimateTokensFromChars(100); got != 33 {
		t.Fatalf("expected 33, got %d", got)
	}
}

func TestEstimateCostUSD(t *testing.T) {
	if got := EstimateCostRUB(1_000_000, 0.02); got != 0.02 {
		t.Fatalf("expected 0.02, got %f", got)
	}
	if got := EstimateCostRUB(500_000, 0.02); got != 0.01 {
		t.Fatalf("expected 0.01, got %f", got)
	}
	if got := EstimateCostRUB(500_000, 0); got != 0 {
		t.Fatalf("expected 0, got %f", got)
	}
}
