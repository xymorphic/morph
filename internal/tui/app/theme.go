package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tuirender "github.com/xymorphic/morph/internal/tui/render"
	"github.com/xymorphic/morph/pkg/str"
	"github.com/xymorphic/morph/pkg/termtheme"
)

type tuiTheme = tuirender.Theme

var detectTerminalTheme = func() termtheme.Result {
	return termtheme.Detect(80 * time.Millisecond)
}

var defaultTUITheme = loadDefaultTUITheme()

func loadDefaultTUITheme() tuiTheme {
	return adaptTUITheme(tuirender.DefaultTheme, detectTerminalTheme())
}

func adaptTUITheme(base tuiTheme, result termtheme.Result) tuiTheme {
	if result.Error != "" {
		return base
	}

	background, ok := parseThemeHexColor(result.Background)
	if !ok {
		return base
	}
	themeValue := str.String(result.Theme)
	switch themeValue.Normalized() {
	case "dark":
		return adaptDarkTUITheme(base, background)
	case "light":
		return adaptLightTUITheme(base, background)
	default:
		return base
	}
}

func adaptDarkTUITheme(base tuiTheme, background themeRGB) tuiTheme {
	theme := base
	theme.CompactionText = mixThemeColor(background, themeRGB{R: 255, G: 255, B: 255}, 0.22).Hex()
	theme.CompactionSeparator = mixThemeColor(background, themeRGB{R: 255, G: 255, B: 255}, 0.11).Hex()
	if isNearBlackThemeColor(background) {
		return theme
	}

	theme.InputFrameBackground = mixThemeColor(background, themeRGB{R: 255, G: 255, B: 255}, 0.06).Hex()
	theme.InputFrameBorder = mixThemeColor(background, themeRGB{R: 255, G: 255, B: 255}, 0.28).Hex()
	theme.UserTranscriptBackground = mixThemeColor(background, themeRGB{R: 255, G: 255, B: 255}, 0.055).Hex()
	theme.Separator = mixThemeColor(background, themeRGB{R: 255, G: 255, B: 255}, 0.22).Hex()
	theme.NoticeBackground = mixThemeColor(background, themeRGB{R: 255, G: 255, B: 255}, 0.10).Hex()
	theme.NoticeBorder = mixThemeColor(background, themeRGB{R: 255, G: 255, B: 255}, 0.30).Hex()
	theme.MarkdownCodeBackground = mixThemeColor(background, themeRGB{R: 255, G: 255, B: 255}, 0.14).Hex()
	theme.JumpToBottomBackground = mixThemeColor(background, themeRGB{R: 255, G: 255, B: 255}, 0.18).Hex()

	return theme
}

func adaptLightTUITheme(base tuiTheme, background themeRGB) tuiTheme {
	theme := base
	theme.CompactionText = mixThemeColor(background, themeRGB{}, 0.42).Hex()
	theme.CompactionSeparator = mixThemeColor(background, themeRGB{}, 0.18).Hex()

	return theme
}

type themeRGB struct {
	R int
	G int
	B int
}

func (c themeRGB) Hex() string {
	return fmt.Sprintf("#%02x%02x%02x", clampThemeColor(c.R), clampThemeColor(c.G), clampThemeColor(c.B))
}

func parseThemeHexColor(value string) (themeRGB, bool) {
	valueText := str.String(value).Trim()
	if len(valueText) != 7 || !strings.HasPrefix(valueText, "#") {
		return themeRGB{}, false
	}

	red, err := strconv.ParseInt(valueText[1:3], 16, 32)
	if err != nil {
		return themeRGB{}, false
	}

	green, err := strconv.ParseInt(valueText[3:5], 16, 32)
	if err != nil {
		return themeRGB{}, false
	}

	blue, err := strconv.ParseInt(valueText[5:7], 16, 32)
	if err != nil {
		return themeRGB{}, false
	}

	return themeRGB{R: int(red), G: int(green), B: int(blue)}, true
}

func isNearBlackThemeColor(color themeRGB) bool {
	return color.R <= 8 && color.G <= 8 && color.B <= 8
}

func mixThemeColor(from themeRGB, to themeRGB, amount float64) themeRGB {
	if amount < 0 {
		amount = 0
	}
	if amount > 1 {
		amount = 1
	}

	return themeRGB{
		R: int(float64(from.R) + (float64(to.R)-float64(from.R))*amount),
		G: int(float64(from.G) + (float64(to.G)-float64(from.G))*amount),
		B: int(float64(from.B) + (float64(to.B)-float64(from.B))*amount),
	}
}

func clampThemeColor(value int) int {
	if value < 0 {
		return 0
	}
	if value > 255 {
		return 255
	}

	return value
}
