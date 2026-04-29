package tui

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

func fetchArtworkCmd(url string, width, height int) tea.Cmd {
	if strings.TrimSpace(url) == "" {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return artworkLoadedMsg{url: url}
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return artworkLoadedMsg{url: url}
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return artworkLoadedMsg{url: url}
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return artworkLoadedMsg{url: url}
		}
		img, _, err := image.Decode(bytes.NewReader(body))
		if err != nil {
			return artworkLoadedMsg{url: url}
		}
		return artworkLoadedMsg{
			url: url,
			art: renderArtworkANSI(img, width, height),
		}
	}
}

func renderArtworkANSI(img image.Image, width, height int) string {
	if img == nil || width <= 0 || height <= 0 {
		return ""
	}

	bounds := img.Bounds()
	if bounds.Dx() == 0 || bounds.Dy() == 0 {
		return ""
	}

	rows := make([]string, 0, (height+1)/2)
	for y := 0; y < height; y += 2 {
		var line strings.Builder
		for x := 0; x < width; x++ {
			top := sampleNearest(img, bounds, x, y, width, height)
			bottom := top
			if y+1 < height {
				bottom = sampleNearest(img, bounds, x, y+1, width, height)
			}
			line.WriteString(colorUpperHalfBlock(top, bottom))
		}
		line.WriteString("\x1b[0m")
		rows = append(rows, line.String())
	}
	return strings.Join(rows, "\n")
}

func sampleNearest(img image.Image, bounds image.Rectangle, x, y, width, height int) color.RGBA {
	srcX := bounds.Min.X + (x*bounds.Dx())/max(1, width)
	srcY := bounds.Min.Y + (y*bounds.Dy())/max(1, height)
	r, g, b, _ := img.At(srcX, srcY).RGBA()
	return color.RGBA{
		R: uint8(r >> 8),
		G: uint8(g >> 8),
		B: uint8(b >> 8),
		A: 255,
	}
}

func colorUpperHalfBlock(top, bottom color.RGBA) string {
	return fmt.Sprintf(
		"\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm▀",
		top.R, top.G, top.B,
		bottom.R, bottom.G, bottom.B,
	)
}
