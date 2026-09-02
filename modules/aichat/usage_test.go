package aichat

import "testing"

func TestRatesAndCost(t *testing.T) {
	u := usageAcc{
		Model:      "gemini-3.8-flash",
		Prompt:     1_000_000,
		Cached:     400_000,
		Candidates: 100_000,
		Thoughts:   50_000,
	}
	// uncached 600k * 0.75 + cached 400k * 0.075 + output 150k * 3.75
	// = 0.45 + 0.03 + 0.5625 = 1.0425
	got := u.costUSD()
	if got < 1.04 || got > 1.05 {
		t.Fatalf("cost = %v", got)
	}
	r := ratesForModel("gemini-3.5-flash-lite")
	if r.In != 0.30 || r.Out != 2.50 {
		t.Fatalf("lite rates %+v", r)
	}
}
