package tui

import (
	"strings"

	"github.com/xymorphic/morph/pkg/str"
)

const transcriptWindowHeightMultiplier = 3

type transcriptWindowState struct {
	startBlock  int
	startLine   int
	endBlock    int
	totalBlocks int
}

type transcriptWindowPosition struct {
	startBlock int
	startLine  int
	offset     int
	atBottom   bool
}

type transcriptBlockCache struct {
	blocks       []transcriptWindowBlock
	generation   uint64
	messageCount int
	baseCount    int
	hasHeader    bool
}

type transcriptWindowBlock struct {
	staticLines    []string
	renderBlock    transcriptRenderBlock
	hasRenderBlock bool
	separator      bool
}

type transcriptWindowMode int

const (
	transcriptWindowCurrent transcriptWindowMode = iota
	transcriptWindowHead
	transcriptWindowTail
)

func (m *model) renderTranscriptWindowIntoViewport(mode transcriptWindowMode) int {
	blocks := m.getTranscriptWindowBlocks()
	target := m.getTranscriptWindowLineTarget()
	start := m.transcriptWindow.startBlock
	startLine := m.transcriptWindow.startLine
	if mode == transcriptWindowHead || start >= len(blocks) {
		start = 0
		startLine = 0
	}
	start, startLine = m.clampTranscriptWindowPosition(blocks, start, startLine)

	var lines []string
	var end int
	prepended := 0
	if mode == transcriptWindowTail {
		start, startLine, lines = m.renderTranscriptWindowTail(blocks, target)
		end = len(blocks)
	} else {
		lines, end = m.renderTranscriptWindowForward(blocks, start, startLine, target)
		if end == len(blocks) && len(lines) < target && start > 0 {
			currentLineCount := len(lines)
			start, startLine, lines = m.renderTranscriptWindowTail(blocks, target)
			prepended = len(lines) - currentLineCount
		}
	}

	m.transcript.SetContentLines(lines)
	m.transcriptWindow = transcriptWindowState{
		startBlock:  start,
		startLine:   startLine,
		endBlock:    end,
		totalBlocks: len(blocks),
	}
	m.transcriptRenders++
	if m.responding {
		m.streamingRenderAt = currentTime()
	}

	return prepended
}

func (m *model) renderTranscriptWindowForScroll(mode transcriptWindowMode) {
	streamingRenderAt := m.streamingRenderAt
	m.renderTranscriptWindowIntoViewport(mode)
	m.streamingRenderAt = streamingRenderAt
}

func (m model) getTranscriptWindowLineTarget() int {
	height := m.transcript.Height()
	if height <= 0 {
		height = defaultHeight
	}

	return height * transcriptWindowHeightMultiplier
}

func (m *model) renderTranscriptWindowForward(
	blocks []transcriptWindowBlock,
	start int,
	startLine int,
	target int,
) ([]string, int) {
	lines := make([]string, 0, target)
	end := start
	line := startLine
	for end < len(blocks) && len(lines) < target {
		blockLines := m.renderTranscriptWindowBlock(blocks[end])
		line = min(max(line, 0), len(blockLines))
		remaining := target - len(lines)
		if len(blockLines)-line > remaining {
			lines = append(lines, blockLines[line:line+remaining]...)
			return lines, end
		}
		lines = append(lines, blockLines[line:]...)
		end++
		line = 0
	}

	return lines, end
}

func (m *model) renderTranscriptWindowTail(
	blocks []transcriptWindowBlock,
	target int,
) (int, int, []string) {
	start := len(blocks)
	startLine := 0
	chunks := make([][]string, 0, target)
	lineCount := 0
	for start > 0 && lineCount < target {
		start--
		blockLines := m.renderTranscriptWindowBlock(blocks[start])
		remaining := target - lineCount
		if len(blockLines) > remaining {
			startLine = len(blockLines) - remaining
			blockLines = blockLines[startLine:]
		}
		chunks = append(chunks, blockLines)
		lineCount += len(blockLines)
	}
	lines := make([]string, 0, lineCount)
	for index := len(chunks) - 1; index >= 0; index-- {
		lines = append(lines, chunks[index]...)
	}

	return start, startLine, lines
}

func (m *model) renderTranscriptWindowBlock(block transcriptWindowBlock) []string {
	lines := block.staticLines
	if block.hasRenderBlock {
		width := max(m.transcript.Width(), m.getMainPaneWidth())
		contentWidth := getPanelContentWidth(width)
		ctx := transcriptRenderContext{
			Width:   contentWidth,
			Padding: getPanelHorizontalPadding(width),
			Frame:   m.toolAnimationFrame,
			Now:     currentTime(),
			Cache:   m.transcriptCache,
		}
		lines = block.renderBlock.renderLines(lipglossTranscriptRenderer{}, ctx)
	}
	if len(lines) == 0 {
		return nil
	}
	if !block.separator {
		return lines
	}

	result := make([]string, 0, len(lines)+1)
	result = append(result, "")
	result = append(result, lines...)
	return result
}

func (m *model) getTranscriptWindowBlocks() []transcriptWindowBlock {
	hasLive := m.live != nil && !m.live.IsEmpty()
	if len(m.messages) == 0 && !hasLive && m.shouldShowNamePrompt() {
		return nil
	}
	if len(m.messages) == 0 && !hasLive && m.shouldShowEmptyUserPrompt() {
		return []transcriptWindowBlock{{staticLines: splitTranscriptLines(m.renderEmptyUserPromptContent())}}
	}
	if len(m.messages) == 0 && !hasLive && m.showIntro {
		m.showIntro = false
		return m.getTranscriptWindowBlocksForCells([]transcriptCell{
			systemTranscriptCell{text: "Welcome to Morph TUI.\n\nThe interactive shell is ready."},
		})
	}
	if len(m.messages) > 0 || hasLive {
		m.showIntro = false
	}
	if _, liveIsTool := m.live.(toolTranscriptCell); hasLive && liveIsTool {
		cells := make([]transcriptCell, 0, len(m.messages)+1)
		cells = append(cells, m.messages...)
		cells = append(cells, m.live)
		return m.getTranscriptWindowBlocksForCells(cells)
	}

	blocks := m.getCachedTranscriptWindowBlocks()
	if !hasLive {
		return blocks
	}
	liveBlocks := getTranscriptRenderBlocks([]transcriptCell{m.live})
	for _, renderBlock := range liveBlocks {
		blocks = append(blocks, transcriptWindowBlock{
			renderBlock:    renderBlock,
			hasRenderBlock: true,
			separator:      len(blocks) > 0,
		})
	}
	m.transcriptBlockCache.blocks = blocks

	return blocks
}

func (m *model) getCachedTranscriptWindowBlocks() []transcriptWindowBlock {
	header := m.getTranscriptHeaderLines()
	cacheCurrent := m.transcriptBlockCache.generation == m.transcriptGeneration &&
		m.transcriptBlockCache.messageCount == len(m.messages) &&
		m.transcriptBlockCache.hasHeader == (len(header) > 0) &&
		m.transcriptBlockCache.baseCount > 0
	if cacheCurrent {
		m.transcriptBlockCache.blocks = m.transcriptBlockCache.blocks[:m.transcriptBlockCache.baseCount]
		if m.transcriptBlockCache.hasHeader {
			m.transcriptBlockCache.blocks[0].staticLines = header
		}
		return m.transcriptBlockCache.blocks
	}

	blocks := make([]transcriptWindowBlock, 0, len(m.messages)+2)
	if len(header) > 0 {
		blocks = append(blocks, transcriptWindowBlock{staticLines: header})
	}
	for _, renderBlock := range getTranscriptRenderBlocks(m.messages) {
		blocks = append(blocks, transcriptWindowBlock{
			renderBlock:    renderBlock,
			hasRenderBlock: true,
			separator:      len(blocks) > 0,
		})
	}
	m.transcriptBlockCache = transcriptBlockCache{
		blocks:       blocks,
		generation:   m.transcriptGeneration,
		messageCount: len(m.messages),
		baseCount:    len(blocks),
		hasHeader:    len(header) > 0,
	}

	return blocks
}

func (m *model) getTranscriptWindowBlocksForCells(cells []transcriptCell) []transcriptWindowBlock {
	blocks := make([]transcriptWindowBlock, 0, len(cells)+1)
	header := m.getTranscriptHeaderLines()
	if len(header) > 0 {
		blocks = append(blocks, transcriptWindowBlock{staticLines: header})
	}
	for _, renderBlock := range getTranscriptRenderBlocks(cells) {
		blocks = append(blocks, transcriptWindowBlock{
			renderBlock:    renderBlock,
			hasRenderBlock: true,
			separator:      len(blocks) > 0,
		})
	}

	return blocks
}

func (m *model) getTranscriptHeaderLines() []string {
	transcriptWidth := m.transcript.Width()
	if transcriptWidth <= 0 {
		transcriptWidth = m.getMainPaneWidth()
	}
	return splitTranscriptLines(strings.Trim(m.renderHeaderWithWidth(transcriptWidth), "\n"))
}

func splitTranscriptLines(content string) []string {
	contentValue := str.String(content)
	if contentValue.Trim() == "" {
		return nil
	}

	return strings.Split(content, "\n")
}

func (m model) isTranscriptAtAbsoluteBottom() bool {
	return m.transcriptWindow.endBlock == m.transcriptWindow.totalBlocks && m.transcript.AtBottom()
}

func (m model) getTranscriptWindowPosition() transcriptWindowPosition {
	return transcriptWindowPosition{
		startBlock: m.transcriptWindow.startBlock,
		startLine:  m.transcriptWindow.startLine,
		offset:     m.transcript.YOffset(),
		atBottom:   m.isTranscriptAtAbsoluteBottom(),
	}
}

func (m *model) getTranscriptAbsoluteTopLine() int {
	blocks := m.getTranscriptWindowBlocks()
	line := 0
	for index := 0; index < min(m.transcriptWindow.startBlock, len(blocks)); index++ {
		line += len(m.renderTranscriptWindowBlock(blocks[index]))
	}

	return line + m.transcriptWindow.startLine + m.transcript.YOffset()
}

func (m *model) getTranscriptRenderedLineCount() int {
	lineCount := 0
	for _, block := range m.getTranscriptWindowBlocks() {
		lineCount += len(m.renderTranscriptWindowBlock(block))
	}

	return lineCount
}

func (m *model) renderTranscriptWindowAtAbsoluteLine(line int) {
	blocks := m.getTranscriptWindowBlocks()
	line = min(max(line, 0), max(m.getTranscriptRenderedLineCount()-1, 0))
	windowStart := max(line-max(m.transcript.Height(), 1), 0)
	startBlock, startLine, moved := m.moveTranscriptWindowPositionForward(
		blocks,
		0,
		0,
		windowStart,
	)
	m.transcriptWindow.startBlock = startBlock
	m.transcriptWindow.startLine = startLine
	streamingRenderAt := m.streamingRenderAt
	prepended := m.renderTranscriptWindowIntoViewport(transcriptWindowCurrent)
	m.streamingRenderAt = streamingRenderAt
	m.transcript.SetYOffset(line - moved + prepended)
}

func (m *model) shiftTranscriptWindowUp() bool {
	if m.selection.active {
		return m.expandTranscriptSelectionWindow(-1)
	}
	if m.transcriptWindow.startBlock == 0 && m.transcriptWindow.startLine == 0 {
		return false
	}

	blocks := m.getTranscriptWindowBlocks()
	oldStart := m.transcriptWindow.startBlock
	oldStartLine := m.transcriptWindow.startLine
	start, startLine, added := m.moveTranscriptWindowPositionBackward(
		blocks,
		oldStart,
		oldStartLine,
		max(m.transcript.Height(), 1),
	)
	lines, end := m.renderTranscriptWindowForward(
		blocks, start, startLine, m.getTranscriptWindowLineTarget(),
	)
	offset := m.transcript.YOffset() + added
	m.transcript.SetContentLines(lines)
	m.transcript.SetYOffset(offset)
	m.transcriptWindow = transcriptWindowState{
		startBlock:  start,
		startLine:   startLine,
		endBlock:    end,
		totalBlocks: len(blocks),
	}

	return start != oldStart || startLine != oldStartLine
}

func (m *model) shiftTranscriptWindowDown() bool {
	if m.selection.active {
		return m.expandTranscriptSelectionWindow(1)
	}
	if m.transcriptWindow.endBlock >= m.transcriptWindow.totalBlocks {
		return false
	}

	blocks := m.getTranscriptWindowBlocks()
	dropLimit := min(max(m.transcript.Height(), 1), max(m.transcript.YOffset()-1, 0))
	if dropLimit == 0 {
		return false
	}
	start, startLine, dropped := m.moveTranscriptWindowPositionForward(
		blocks,
		m.transcriptWindow.startBlock,
		m.transcriptWindow.startLine,
		dropLimit,
	)
	lines, end := m.renderTranscriptWindowForward(
		blocks, start, startLine, m.getTranscriptWindowLineTarget(),
	)
	offset := max(m.transcript.YOffset()-dropped, 0)
	m.transcript.SetContentLines(lines)
	m.transcript.SetYOffset(offset)
	m.transcriptWindow = transcriptWindowState{
		startBlock:  start,
		startLine:   startLine,
		endBlock:    end,
		totalBlocks: len(blocks),
	}

	return true
}

func (m *model) moveTranscriptWindowPositionBackward(
	blocks []transcriptWindowBlock,
	block int,
	line int,
	distance int,
) (int, int, int) {
	block, line = m.clampTranscriptWindowPosition(blocks, block, line)
	moved := 0
	for distance > 0 && (block > 0 || line > 0) {
		if line == 0 {
			block--
			line = len(m.renderTranscriptWindowBlock(blocks[block]))
		}
		step := min(line, distance)
		line -= step
		distance -= step
		moved += step
	}

	return block, line, moved
}

func (m *model) clampTranscriptWindowPosition(
	blocks []transcriptWindowBlock,
	block int,
	line int,
) (int, int) {
	block = min(max(block, 0), len(blocks))
	if block == len(blocks) {
		return block, 0
	}

	return block, min(max(line, 0), len(m.renderTranscriptWindowBlock(blocks[block])))
}

func (m *model) moveTranscriptWindowPositionForward(
	blocks []transcriptWindowBlock,
	block int,
	line int,
	distance int,
) (int, int, int) {
	moved := 0
	for distance > 0 && block < len(blocks) {
		blockLines := m.renderTranscriptWindowBlock(blocks[block])
		available := len(blockLines) - min(max(line, 0), len(blockLines))
		if available == 0 {
			block++
			line = 0
			continue
		}
		step := min(available, distance)
		line += step
		distance -= step
		moved += step
		if line == len(blockLines) {
			block++
			line = 0
		}
	}

	return block, line, moved
}
