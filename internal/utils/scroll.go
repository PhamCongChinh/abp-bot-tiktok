package utils

import (
	"math/rand"

	"github.com/playwright-community/playwright-go"
)

// HumanScroll simulates human-like scrolling behavior
// Mirrors scraper_tt scroll_utils.py: uses grid-item-container locator + scroll_into_view
func HumanScroll(page playwright.Page, times int) error {
	locator := page.Locator("[id^='grid-item-container-']")

	for i := 0; i < times; i++ {
		count, err := locator.Count()
		if err != nil || count == 0 {
			// Fallback: mouse wheel if no items found yet
			page.Mouse().Wheel(0, float64(RandInt(400, 800)))
			Sleep(800, 1500)
			continue
		}

		// Move mouse slightly (like a human)
		page.Mouse().Move(
			float64(RandInt(200, 600)),
			float64(RandInt(200, 500)),
		)

		// Scroll to item near the bottom of visible list (same as scraper_tt: min(count-1, 10))
		index := count - 1
		if index > 10 {
			index = 10
		}
		item := locator.Nth(index)
		if err := item.ScrollIntoViewIfNeeded(playwright.LocatorScrollIntoViewIfNeededOptions{
			Timeout: playwright.Float(3000),
		}); err != nil {
			// Fallback to mouse wheel
			page.Mouse().Wheel(0, float64(RandInt(400, 800)))
		}

		Sleep(1200, 2000)

		// 15% chance: scroll back up slightly (human behavior - re-read something)
		if rand.Float64() < 0.15 {
			page.Mouse().Wheel(0, float64(-RandInt(80, 180)))
			Sleep(500, 1000)
		}

		// 10% chance: long pause (user got distracted)
		if rand.Float64() < 0.1 {
			Sleep(3000, 6000)
		}

		Sleep(800, 1500)
	}

	return nil
}

// RandomMouseMove moves mouse to a random position
func RandomMouseMove(page playwright.Page) error {
	return page.Mouse().Move(
		float64(RandInt(100, 900)),
		float64(RandInt(100, 600)),
		playwright.MouseMoveOptions{
			Steps: playwright.Int(RandInt(10, 30)),
		},
	)
}

// RandomViewVideo simulates human video viewing behavior
// Mirrors scraper_tt browser_actions.py: random_view_video
func RandomViewVideo(page playwright.Page) error {
	locator := page.Locator("[id^='grid-item-container-']")
	count, err := locator.Count()
	if err != nil || count == 0 {
		return nil
	}

	// 70% chance: scroll a bit before selecting
	if rand.Float64() < 0.7 {
		page.Mouse().Wheel(0, float64(RandInt(200, 700)))
		Sleep(800, 2000)
	}

	// 60% chance to interact
	if rand.Float64() >= 0.6 {
		return nil
	}

	maxCandidates := 5
	if count-1 < maxCandidates {
		maxCandidates = count - 1
	}
	index := rand.Intn(maxCandidates + 1)
	video := locator.Nth(index)

	originalURL := page.URL()

	// Scroll to video
	video.ScrollIntoViewIfNeeded()
	Sleep(800, 2000)

	// Get bounding box
	box, err := video.BoundingBox()
	if err == nil && box != nil {
		startX := box.X + float64(RandInt(5, int(box.Width-5)))
		startY := box.Y + float64(RandInt(5, int(box.Height-5)))

		// Multi-step mouse movement
		for i := 0; i < RandInt(2, 4); i++ {
			x := startX + float64(RandInt(-30, 30))
			y := startY + float64(RandInt(-30, 30))
			page.Mouse().Move(x, y, playwright.MouseMoveOptions{
				Steps: playwright.Int(RandInt(5, 15)),
			})
			Sleep(200, 600)
		}

		page.Mouse().Move(startX, startY, playwright.MouseMoveOptions{
			Steps: playwright.Int(RandInt(10, 20)),
		})
	} else {
		video.Hover(playwright.LocatorHoverOptions{Force: playwright.Bool(true)})
	}
	Sleep(1000, 2500)

	// 30% chance: only hover, no click
	if rand.Float64() < 0.3 {
		return nil
	}

	// Click
	if box != nil {
		clickX := box.X + float64(RandInt(10, int(box.Width-10)))
		clickY := box.Y + float64(RandInt(10, int(box.Height-10)))
		page.Mouse().Click(clickX, clickY)
	} else {
		video.Click(playwright.LocatorClickOptions{Force: playwright.Bool(true)})
	}

	// Watch video 8-25 seconds
	Sleep(8000, 25000)

	// 30% chance: scroll comments
	if rand.Float64() < 0.3 {
		page.Mouse().Wheel(0, float64(RandInt(300, 800)))
		Sleep(2000, 5000)
	}

	// Go back - always try to return to original URL
	if currentURL := page.URL(); currentURL != originalURL {
		if _, err := page.GoBack(); err != nil {
			// Fallback: navigate directly back
			page.Goto(originalURL, playwright.PageGotoOptions{
				WaitUntil: playwright.WaitUntilStateDomcontentloaded,
				Timeout:   playwright.Float(60000),
			})
		} else {
			page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
				State: playwright.LoadStateDomcontentloaded,
			})
		}
		Sleep(3000, 6000)
	}

	return nil
}
