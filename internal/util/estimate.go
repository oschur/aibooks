package util

// кол-во токенов ~= кол-во символов / 3 (приблизительно)
// для русского языка один токен равнеяется около 2 символов, для английского 4 символа, берем среднее значение 3 символа на токен
func EstimateTokensFromChars(chars int) int {
	if chars <= 0 {
		return 0
	}
	return chars / 3
}

func EstimateCostRUB(tokens int, costPer1MTokensRUB float64) float64 {
	if tokens <= 0 || costPer1MTokensRUB <= 0 {
		return 0
	}
	return (float64(tokens) / 1_000_000.0) * costPer1MTokensRUB
}
