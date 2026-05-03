package utils

import (
	"math/rand"

	"github.com/playwright-community/playwright-go"
)

// HumanScroll simulates human-like scrolling behavior
func HumanScroll(page playwright.Page, times int) error {
	for i := 0; i < times; i++ {
		scrollAmount := RandInt(800, 1200)
		
		// Method 1: Scroll main container
		page.Evaluate(`(amount) => {
			const container = document.querySelector('#main-content-search_top') 
				|| document.querySelector('[data-e2e="search_top-item-list"]')
				|| document.querySelector('div[class*="DivItemContainer"]')
				|| document.body;
			
			if (container) {
				container.scrollTop = container.scrollTop + amount;
			}
		}`, scrollAmount)
		
		// Method 2: Window scroll as backup
		page.Evaluate(`(amount) => window.scrollBy(0, amount)`, scrollAmount)
		
		Sleep(2000, 3000)

		// Method 3: Scroll to last visible video
		page.Evaluate(`
			const videos = document.querySelectorAll('[data-e2e="search-common-video"], [class*="DivItemContainer"], div[class*="video"]');
			if (videos.length > 3) {
				videos[videos.length - 2].scrollIntoView({ behavior: 'smooth', block: 'center' });
			}
		`)
		
		Sleep(1500, 2500)

		// 20% chance: scroll back up
		if rand.Float64() < 0.2 {
			page.Evaluate(`window.scrollBy(0, -300)`)
			Sleep(500, 800)
		}

		// 10% chance: long pause
		if rand.Float64() < 0.1 {
			Sleep(3000, 6000)
		}

		Sleep(1000, 1500)
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

		// Final hover
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

	// 25% chance: double-click to like (heart)
	if rand.Float64() < 0.25 {
		if box != nil {
			likeX := box.X + float64(RandInt(10, int(box.Width-10)))
			likeY := box.Y + float64(RandInt(10, int(box.Height-10)))
			page.Mouse().Dblclick(likeX, likeY)
			Sleep(500, 1000)
		}
	}

	// 30% chance: scroll comments
	if rand.Float64() < 0.3 {
		page.Mouse().Wheel(0, float64(RandInt(300, 800)))
		Sleep(2000, 5000)
	}

	// Go back
	if _, err := page.GoBack(); err != nil {
		return err
	}
	page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
		State: playwright.LoadStateDomcontentloaded,
	})
	Sleep(3000, 6000)

	return nil
}
