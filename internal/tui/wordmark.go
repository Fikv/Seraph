package tui

import "time"

const spinStep = 130 * time.Millisecond

var spinEpoch = time.Now()

func spinNow(reverse bool) string {
	return spinStar(int(time.Since(spinEpoch)/spinStep)*spinEvery, reverse)
}

func spinTitle() string {
	return sGoldB.Render(spinNow(false) + " SERAPH " + spinNow(true))
}
