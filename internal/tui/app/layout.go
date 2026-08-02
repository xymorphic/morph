package tui

import tuilayout "github.com/xymorphic/morph/internal/tui/layout"

type tuiRect struct {
	X      int
	Y      int
	Width  int
	Height int
}

type tuiLayout struct {
	Transcript         tuiRect
	JumpToBottom       tuiRect
	SessionQueue       tuiRect
	Composer           tuiRect
	BottomStatusPanel  tuiRect
	PanelContentWidth  int
	PanelHorizontalPad int
}

func getTUILayout(width int, height int, inputHeight int) tuiLayout {
	return getTUILayoutWithInputChromeHeight(width, height, inputHeight, baseInputChromeHeight)
}

func getTUILayoutWithInputChromeHeight(width int, height int, inputHeight int, chromeHeight int) tuiLayout {
	return tuiLayoutFromRegions(tuilayout.Compute(width, height, inputHeight, tuilayout.Metrics{
		MinInputHeight:              minInputHeight,
		InputChromeHeight:           chromeHeight,
		InputFrameChromeHeight:      inputFrameChromeHeight,
		TranscriptComposerGapHeight: transcriptComposerGapHeight,
		BottomStatusPanelHeight:     bottomStatusPanelHeight,
	}))
}

func getTUILayoutWithBottomPaneHeight(width int, height int, bottomPaneHeight int) tuiLayout {
	return tuiLayoutFromRegions(tuilayout.Compute(width, height, bottomPaneHeight, tuilayout.Metrics{
		MinInputHeight:              1,
		InputChromeHeight:           transcriptComposerGapHeight,
		InputFrameChromeHeight:      0,
		TranscriptComposerGapHeight: transcriptComposerGapHeight,
		BottomStatusPanelHeight:     0,
	}))
}

func getMainPaneWidth(width int) int {
	return max(width, 1)
}

func tuiLayoutFromRegions(regions tuilayout.Regions) tuiLayout {
	return tuiLayout{
		Transcript:         tuiRectFromLayoutRect(regions.Transcript),
		JumpToBottom:       tuiRectFromLayoutRect(regions.JumpToBottom),
		Composer:           tuiRectFromLayoutRect(regions.Composer),
		BottomStatusPanel:  tuiRectFromLayoutRect(regions.BottomStatusPanel),
		PanelContentWidth:  regions.PanelContentWidth,
		PanelHorizontalPad: regions.PanelHorizontalPad,
	}
}

func tuiRectFromLayoutRect(rect tuilayout.Rect) tuiRect {
	return tuiRect{
		X:      rect.X,
		Y:      rect.Y,
		Width:  rect.Width,
		Height: rect.Height,
	}
}
