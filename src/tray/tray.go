// Package tray implements the Windows system tray icon for OfficeClaw.
// The agent runs minimized to the system tray with status and controls.
package tray

import (
	"context"
	"log"
	"os"
	"path/filepath"

	"github.com/getlantern/systray"

	"github.com/officeclaw/src/config"
)

// generateDefaultIcon creates a 16x16 claw-shaped ICO icon programmatically.
func generateDefaultIcon() []byte {
	const width = 16
	const height = 16

	// Colors (BGRA format)
	transparent := [4]byte{0x00, 0x00, 0x00, 0x00}
	orange := [4]byte{0x00, 0x8C, 0xFF, 0xFF}      // Bright orange (#FF8C00)
	darkOrange := [4]byte{0x00, 0x45, 0xCC, 0xFF}  // Dark orange (#CC4500)

	// 16x16 claw icon pattern (0=transparent, 1=orange, 2=dark orange)
	// Displayed bottom-up in ICO format, so we define top-down and reverse
	pattern := [][]byte{
		{0, 0, 0, 1, 0, 0, 1, 0, 0, 1, 0, 0, 1, 0, 0, 0},
		{0, 0, 0, 1, 0, 0, 1, 0, 0, 1, 0, 0, 1, 0, 0, 0},
		{0, 0, 0, 1, 0, 0, 1, 0, 0, 1, 0, 0, 1, 0, 0, 0},
		{0, 0, 0, 2, 1, 0, 2, 1, 1, 2, 0, 1, 2, 0, 0, 0},
		{0, 0, 0, 0, 2, 1, 0, 2, 2, 0, 1, 2, 0, 0, 0, 0},
		{0, 0, 0, 0, 0, 2, 1, 1, 1, 1, 2, 0, 0, 0, 0, 0},
		{0, 0, 0, 0, 0, 0, 2, 1, 1, 2, 0, 0, 0, 0, 0, 0},
		{0, 0, 0, 0, 0, 0, 2, 1, 1, 2, 0, 0, 0, 0, 0, 0},
		{0, 0, 0, 0, 0, 0, 2, 1, 1, 2, 0, 0, 0, 0, 0, 0},
		{0, 0, 0, 0, 0, 0, 2, 1, 1, 2, 0, 0, 0, 0, 0, 0},
		{0, 0, 0, 0, 0, 1, 2, 1, 1, 2, 1, 0, 0, 0, 0, 0},
		{0, 0, 0, 0, 1, 2, 0, 1, 1, 0, 2, 1, 0, 0, 0, 0},
		{0, 0, 0, 1, 2, 0, 0, 1, 1, 0, 0, 2, 1, 0, 0, 0},
		{0, 0, 1, 2, 0, 0, 0, 1, 1, 0, 0, 0, 2, 1, 0, 0},
		{0, 0, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 0, 0},
		{0, 0, 0, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 0, 0, 0},
	}

	// Build pixel data (bottom-up for ICO)
	pixelData := make([]byte, width*height*4)
	andMask := make([]byte, 64) // 16 rows * 4 bytes

	for y := 0; y < height; y++ {
		// ICO is bottom-up, so row 0 in pixelData is the bottom row of the image
		srcY := height - 1 - y
		for x := 0; x < width; x++ {
			idx := (y*width + x) * 4
			switch pattern[srcY][x] {
			case 0:
				copy(pixelData[idx:], transparent[:])
				// Set AND mask bit for transparent pixels
				byteIdx := y*4 + x/8
				bitIdx := 7 - (x % 8)
				andMask[byteIdx] |= (1 << bitIdx)
			case 1:
				copy(pixelData[idx:], orange[:])
			case 2:
				copy(pixelData[idx:], darkOrange[:])
			}
		}
	}

	// ICONDIR (6 bytes)
	iconDir := []byte{0x00, 0x00, 0x01, 0x00, 0x01, 0x00}

	// BITMAPINFOHEADER (40 bytes)
	bmpHeader := []byte{
		0x28, 0x00, 0x00, 0x00, // biSize: 40
		0x10, 0x00, 0x00, 0x00, // biWidth: 16
		0x20, 0x00, 0x00, 0x00, // biHeight: 32
		0x01, 0x00,             // biPlanes: 1
		0x20, 0x00,             // biBitCount: 32
		0x00, 0x00, 0x00, 0x00, // biCompression: 0
		0x00, 0x04, 0x00, 0x00, // biSizeImage: 1024
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	}

	imageSize := len(bmpHeader) + len(pixelData) + len(andMask)
	imageOffset := len(iconDir) + 16

	// ICONDIRENTRY (16 bytes)
	iconEntry := []byte{
		byte(width), byte(height), 0x00, 0x00,
		0x01, 0x00, 0x20, 0x00,
		byte(imageSize), byte(imageSize >> 8), byte(imageSize >> 16), byte(imageSize >> 24),
		byte(imageOffset), byte(imageOffset >> 8), byte(imageOffset >> 16), byte(imageOffset >> 24),
	}

	// Assemble ICO
	ico := make([]byte, 0, imageOffset+imageSize)
	ico = append(ico, iconDir...)
	ico = append(ico, iconEntry...)
	ico = append(ico, bmpHeader...)
	ico = append(ico, pixelData...)
	ico = append(ico, andMask...)

	return ico
}

// defaultIcon is generated at init time
var defaultIcon = generateDefaultIcon()

// Run starts the system tray icon. This blocks on the main goroutine
// (required by Windows GUI threading). Call cancel() to trigger shutdown.
func Run(cfg *config.Config, cancel context.CancelFunc, logger *log.Logger) {
	systray.Run(func() {
		onReady(cfg, cancel, logger)
	}, func() {
		onExit(logger)
	})
}

func onReady(cfg *config.Config, cancel context.CancelFunc, logger *log.Logger) {
	systray.SetTitle("OfficeClaw")
	systray.SetTooltip("OfficeClaw AI Agent - Running")

	// Try to load icon from file (icon.ico in executable directory)
	if iconData := loadIcon(); iconData != nil {
		systray.SetIcon(iconData)
	}

	// Menu items
	mStatus := systray.AddMenuItem("Status: Running", "Agent status")
	mStatus.Disable()

	systray.AddSeparator()

	mProvider := systray.AddMenuItem("LLM: "+cfg.LLM.Provider, "Current LLM provider")
	mProvider.Disable()

	mPoll := systray.AddMenuItem("Polling: active", "Listener status")
	mPoll.Disable()

	systray.AddSeparator()

	mQuit := systray.AddMenuItem("Quit OfficeClaw", "Shut down the agent")

	// Handle menu clicks
	go func() {
		<-mQuit.ClickedCh
		logger.Printf("[tray] Quit requested by user")
		cancel()
		systray.Quit()
	}()

	logger.Printf("[tray] System tray icon ready")
}

func onExit(logger *log.Logger) {
	logger.Printf("[tray] System tray exiting")
}

// loadIcon attempts to load icon.ico from the executable directory.
// Falls back to embedded defaultIcon if no file is found.
func loadIcon() []byte {
	// Try to find icon.ico relative to the executable
	execPath, err := os.Executable()
	if err != nil {
		return defaultIcon
	}
	iconPath := filepath.Join(filepath.Dir(execPath), "icon.ico")
	data, err := os.ReadFile(iconPath)
	if err != nil {
		// Try current directory as fallback
		data, err = os.ReadFile("icon.ico")
		if err != nil {
			return defaultIcon
		}
	}
	return data
}
