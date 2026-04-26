package utils

import (
	"math/rand"

	"github.com/playwright-community/playwright-go"
)

// HumanScroll simulates human-like scrolling on a page
func HumanScroll(page playwright.Page, times int) error {
	for i := 0; i < times; i++ {
		scrollY := RandInt(300, 700)
		if err := page.Mouse().Wheel(0, float64(scrollY)); err != nil {
			return err
		}
		Sleep(800, 2000)
	}
	return nil
}

// RandomMouseMove moves mouse to a random position
func RandomMouseMove(page playwright.Page) error {
	x := float64(RandInt(100, 900))
	y := float64(RandInt(100, 600))
	steps := RandInt(10, 30)
	return page.Mouse().Move(x, y, playwright.MouseMoveOptions{
		Steps: playwright.Int(steps),
	})
}

// RandomViewVideo clicks a random video on the page and waits briefly
func RandomViewVideo(page playwright.Page) error {
	videos, err := page.Locator("[id^='grid-item-container-']").All()
	if err != nil || len(videos) == 0 {
		return nil
	}
	// pick a random one
	idx := rand.Intn(len(videos))
	_ = videos[idx].Click()
	Sleep(3000, 6000)
	// go back
	if _, err := page.GoBack(); err != nil {
		return err
	}
	Sleep(1000, 2000)
	return nil
}
