package slack

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/xymorphic/morph/internal/config"
	slack "github.com/xymorphic/morph/pkg/gateway/slack"
	"github.com/xymorphic/morph/pkg/str"
)

const defaultSlackStreamFlushInterval = 150 * time.Millisecond

type Sender struct {
	api API
}

func NewSender(api API) *Sender {
	return &Sender{api: api}
}

func SendFinal(
	ctx context.Context,
	cfg config.GatewaySlackConfig,
	target slack.Target,
	text string,
) error {
	return NewSender(NewHTTPClient(cfg.BotToken)).SendFinal(ctx, target, text)
}

func (s *Sender) SendFinal(ctx context.Context, target slack.Target, text string) error {
	if s == nil || s.api == nil {
		return errors.New("slack sender is required")
	}

	for _, chunk := range slack.ChunkMarkdown(slack.FormatMrkdwn(text), slack.MarkdownTextLimit) {
		if _, err := s.api.PostMessage(ctx, target, chunk); err != nil {
			return err
		}
	}

	return nil
}

func (s *Sender) StreamTurn(
	ctx context.Context,
	target slack.Target,
	run func(func(string)) (string, error),
) error {
	if s == nil || s.api == nil {
		return errors.New("slack sender is required")
	}

	stream, err := s.api.StartStream(ctx, target, "")
	tSValue := str.String(stream.TS)
	if err != nil || tSValue.Trim() == "" {
		reply, runErr := run(func(string) {})
		if runErr != nil {
			return runErr
		}
		return s.SendFinal(ctx, target, reply)
	}

	appender := newSlackStreamAppender(ctx, s.api, stream, defaultSlackStreamFlushInterval)
	appender.Start()
	reply, err := run(appender.Append)
	state, appendErr := appender.Stop()
	if err != nil {
		return err
	}
	if !state.visible && appendErr != nil {
		if isSlackStreamTerminalError(appendErr) {
			return nil
		}

		return s.SendFinal(ctx, target, reply)
	}
	stopText := reply
	if state.visible {
		stopText = ""
	}
	if stopErr := s.api.StopStream(ctx, stream, stopText); stopErr != nil {
		if state.visible {
			if isSlackStreamNonRetryableAfterVisible(stopErr) {
				return nil
			}

			return stopErr
		}
		if isSlackStreamTerminalError(stopErr) {
			return nil
		}

		return s.SendFinal(ctx, target, reply)
	}

	return nil
}

type slackStreamAppender struct {
	ctx      context.Context
	api      API
	stream   slack.Stream
	interval time.Duration
	done     chan struct{}
	stopped  chan struct{}

	mu      sync.Mutex
	pending []slack.Chunk
	format  *slackStreamFormatter
	visible bool
	err     error
}

type slackStreamState struct {
	visible bool
}

func newSlackStreamAppender(
	ctx context.Context,
	api API,
	stream slack.Stream,
	interval time.Duration,
) *slackStreamAppender {
	if interval <= 0 {
		interval = defaultSlackStreamFlushInterval
	}

	return &slackStreamAppender{
		ctx:      ctx,
		api:      api,
		stream:   stream,
		interval: interval,
		done:     make(chan struct{}),
		stopped:  make(chan struct{}),
		format:   &slackStreamFormatter{},
	}
}

func (a *slackStreamAppender) Start() {
	go func() {
		ticker := time.NewTicker(a.interval)
		defer ticker.Stop()
		defer close(a.stopped)

		for {
			select {
			case <-a.ctx.Done():
				a.appendChunks(a.format.Flush())
				a.flush()
				return
			case <-a.done:
				a.flush()
				return
			case <-ticker.C:
				a.flush()
			}
		}
	}()
}

func (a *slackStreamAppender) Append(delta string) {
	if delta == "" {
		return
	}

	chunks := a.format.Append(delta)
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.err != nil {
		return
	}
	if len(chunks) == 0 {
		return
	}
	a.pending = append(a.pending, chunks...)
}

func (a *slackStreamAppender) Stop() (slackStreamState, error) {
	a.appendChunks(a.format.Flush())
	close(a.done)
	<-a.stopped

	a.mu.Lock()
	defer a.mu.Unlock()

	return slackStreamState{visible: a.visible}, a.err
}

func (a *slackStreamAppender) appendChunks(chunks []slack.Chunk) {
	if len(chunks) == 0 {
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if a.err != nil {
		return
	}
	a.pending = append(a.pending, chunks...)
}

func (a *slackStreamAppender) flush() {
	for {
		chunk, ok := a.nextChunk()
		if !ok {
			return
		}

		if err := a.api.AppendStream(a.ctx, a.stream, []slack.Chunk{chunk}); err != nil {
			a.mu.Lock()
			a.err = err
			a.pending = nil
			a.mu.Unlock()
			return
		}

		a.mu.Lock()
		a.visible = true
		a.mu.Unlock()
	}
}

func (a *slackStreamAppender) nextChunk() (slack.Chunk, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.err != nil || len(a.pending) == 0 {
		return slack.Chunk{}, false
	}

	chunk := a.pending[0]
	a.pending = a.pending[1:]
	return chunk, true
}

func getSlackStreamChunks(text string) []slack.Chunk {
	textValue := str.String(text)
	if textValue.Trim() == "" {
		return nil
	}

	runes := []rune(text)
	chunks := make([]slack.Chunk, 0, len(runes)/slack.MarkdownTextLimit+1)
	for len(runes) > 0 {
		n := min(len(runes), slack.MarkdownTextLimit)
		chunks = append(chunks, slack.MarkdownTextChunk(string(runes[:n])))
		runes = runes[n:]
	}

	return chunks
}

func isSlackStreamNonRetryableAfterVisible(err error) bool {
	if err == nil {
		return false
	}
	if isSlackStreamTerminalError(err) {
		return true
	}

	code := getSlackAPIErrorCode(err)
	return code == "rate_limited" || strings.HasPrefix(code, "http_status_")
}

func isSlackStreamTerminalError(err error) bool {
	switch getSlackAPIErrorCode(err) {
	case "stopped_by_user", "message_not_in_streaming_state", "streaming_state_conflict":
		return true
	default:
		return false
	}
}

func getSlackAPIErrorCode(err error) string {
	if err == nil {
		return ""
	}

	if apiErr, ok := errors.AsType[slackAPIError](err); ok {
		codeValue := str.String(apiErr.Code)
		return codeValue.Trim()
	}

	errorValue := str.String(err.Error())
	return errorValue.Trim()
}

type slackStreamFormatter struct {
	buffer string
}

func (f *slackStreamFormatter) Append(delta string) []slack.Chunk {
	f.buffer += delta
	return f.drain(false)
}

func (f *slackStreamFormatter) Flush() []slack.Chunk {
	return f.drain(true)
}

func (f *slackStreamFormatter) drain(final bool) []slack.Chunk {
	if f.buffer == "" {
		return nil
	}

	safeIndex := getSlackStreamSafeFormatIndex(f.buffer, final)
	if safeIndex == 0 {
		return nil
	}

	text := f.buffer[:safeIndex]
	f.buffer = f.buffer[safeIndex:]
	return slack.FormatStreamChunks(text)
}

func getSlackStreamSafeFormatIndex(text string, final bool) int {
	if final {
		return len(text)
	}

	safeIndex := 0
	inFence := false
	lineStart := 0
	closedFenceEnd := 0
	for lineStart < len(text) {
		newline := strings.IndexByte(text[lineStart:], '\n')
		if newline < 0 {
			break
		}

		lineEnd := lineStart + newline + 1
		trimmedValue := str.String(text[lineStart : lineEnd-1])
		line := trimmedValue.Trim()
		if strings.HasPrefix(line, "```") {
			inFence = !inFence
			if !inFence {
				closedFenceEnd = lineEnd
			}
		}

		if !inFence && line == "" {
			safeIndex = lineEnd
		} else if !inFence && closedFenceEnd > 0 && lineEnd > closedFenceEnd {
			safeIndex = closedFenceEnd
			closedFenceEnd = 0
		}
		lineStart = lineEnd
	}
	if !inFence && closedFenceEnd > 0 && len(text) > closedFenceEnd {
		safeIndex = closedFenceEnd
	}

	return safeIndex
}
