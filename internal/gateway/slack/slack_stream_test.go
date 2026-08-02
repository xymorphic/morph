package slack

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/config"
	pkgslack "github.com/xymorphic/morph/pkg/gateway/slack"
)

func TestSender_StreamTurnUsesNativeSlackStream(t *testing.T) {
	api := &fakeSlackAPI{}
	sender := NewSender(api)
	target := slackTestTarget()

	err := sender.StreamTurn(context.Background(), target, func(onDelta func(string)) (string, error) {
		onDelta("hello ")
		onDelta("world")
		return "final", nil
	})

	require.NoError(t, err)
	require.Equal(t, []slackAPICall{
		{method: "startStream", target: target},
		{method: "appendStream", stream: pkgslack.Stream{ChannelID: "C1", TS: "stream-ts"}, text: "hello world"},
		{method: "stopStream", stream: pkgslack.Stream{ChannelID: "C1", TS: "stream-ts"}},
	}, api.allCalls())
}

func TestSender_StreamTurnNormalizesSlackStreamingStrikethrough(t *testing.T) {
	api := &fakeSlackAPI{}
	sender := NewSender(api)
	target := slackTestTarget()

	err := sender.StreamTurn(context.Background(), target, func(onDelta func(string)) (string, error) {
		onDelta("**bold** and ~gone~ and ~~already gone~~ and [Morph](https://example.com)")
		return "final", nil
	})

	require.NoError(t, err)
	require.Equal(t, []slackAPICall{
		{method: "startStream", target: target},
		{
			method: "appendStream",
			stream: pkgslack.Stream{ChannelID: "C1", TS: "stream-ts"},
			text:   "**bold** and ~~gone~~ and ~~already gone~~ and [Morph](https://example.com)",
		},
		{method: "stopStream", stream: pkgslack.Stream{ChannelID: "C1", TS: "stream-ts"}},
	}, api.allCalls())
}

func TestSender_StreamTurnPreservesMentionLineBreaks(t *testing.T) {
	api := &fakeSlackAPI{}
	sender := NewSender(api)
	target := slackTestTarget()

	err := sender.StreamTurn(context.Background(), target, func(onDelta func(string)) (string, error) {
		onDelta("Mention-style examples:\n<@U12345678> user mention\n<!here> here mention\n")
		return "final", nil
	})

	require.NoError(t, err)
	require.Equal(t, []slackAPICall{
		{method: "startStream", target: target},
		{
			method: "appendStream",
			stream: pkgslack.Stream{ChannelID: "C1", TS: "stream-ts"},
			text:   "Mention-style examples:\n<@U12345678> user mention\n<!here> here mention\n",
		},
		{method: "stopStream", stream: pkgslack.Stream{ChannelID: "C1", TS: "stream-ts"}},
	}, api.allCalls())
}

func TestSender_StreamTurnStreamsFencedCodeAsMarkdownText(t *testing.T) {
	api := &fakeSlackAPI{}
	sender := NewSender(api)
	target := slackTestTarget()

	err := sender.StreamTurn(context.Background(), target, func(onDelta func(string)) (string, error) {
		reply := "Code block\n\n```go\nif a < b {\n}\n```\n\nNext"
		for _, delta := range []string{
			"Code block\n\n",
			"```go\n",
			"if a < b {\n",
			"}\n",
			"```\n",
			"\nNext",
		} {
			onDelta(delta)
		}

		return reply, nil
	})

	require.NoError(t, err)
	require.Equal(t, []slackAPICall{
		{method: "startStream", target: target},
		{
			method: "appendStream",
			stream: pkgslack.Stream{ChannelID: "C1", TS: "stream-ts"},
			text:   "Code block\n\n",
		},
		{
			method: "appendStream",
			stream: pkgslack.Stream{ChannelID: "C1", TS: "stream-ts"},
			text:   "```\nif a < b {\n}\n```",
		},
		{
			method: "appendStream",
			stream: pkgslack.Stream{ChannelID: "C1", TS: "stream-ts"},
			text:   "\n",
		},
		{
			method: "appendStream",
			stream: pkgslack.Stream{ChannelID: "C1", TS: "stream-ts"},
			text:   "\nNext",
		},
		{method: "stopStream", stream: pkgslack.Stream{ChannelID: "C1", TS: "stream-ts"}},
	}, api.allCalls())
}

func TestSender_StreamTurnKeepsFencedCodePackageAndImportLines(t *testing.T) {
	api := &fakeSlackAPI{}
	sender := NewSender(api)
	target := slackTestTarget()

	err := sender.StreamTurn(context.Background(), target, func(onDelta func(string)) (string, error) {
		reply := "```go\npackage main\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"Hello, world!\")\n}\n```"
		for _, delta := range []string{
			"```go\n",
			"package main\n",
			"import \"fmt\"\n\n",
			"func main() {\n",
			"\tfmt.Println(\"Hello, world!\")\n",
			"}\n",
			"```",
		} {
			onDelta(delta)
		}

		return reply, nil
	})

	require.NoError(t, err)
	require.Equal(t, []slackAPICall{
		{method: "startStream", target: target},
		{
			method: "appendStream",
			stream: pkgslack.Stream{ChannelID: "C1", TS: "stream-ts"},
			text:   "```\npackage main\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"Hello, world!\")\n}\n```",
		},
		{method: "stopStream", stream: pkgslack.Stream{ChannelID: "C1", TS: "stream-ts"}},
	}, api.allCalls())
}

func TestSender_StreamTurnDoesNotBlockRunOnAppendLatency(t *testing.T) {
	api := &fakeSlackAPI{appendDelay: 100 * time.Millisecond}
	sender := NewSender(api)
	var runElapsed time.Duration

	err := sender.StreamTurn(context.Background(), slackTestTarget(), func(onDelta func(string)) (string, error) {
		start := time.Now()
		onDelta("hello ")
		onDelta("world")
		runElapsed = time.Since(start)
		return "final", nil
	})

	require.NoError(t, err)
	require.Less(t, runElapsed, 50*time.Millisecond)
	require.Equal(t, []string{"startStream", "appendStream", "stopStream"}, api.callMethods())
}

func TestSender_StreamTurnFallsBackWhenStartFails(t *testing.T) {
	api := &fakeSlackAPI{startErr: errSlackTest}
	sender := NewSender(api)
	target := slackTestTarget()

	err := sender.StreamTurn(context.Background(), target, func(onDelta func(string)) (string, error) {
		onDelta("ignored")
		return "final", nil
	})

	require.NoError(t, err)
	require.Equal(t, []slackAPICall{
		{method: "startStream", target: target},
		{method: "postMessage", target: target, text: "final"},
	}, api.allCalls())
}

func TestSender_StreamTurnReturnsRunErrorWhenStartFails(t *testing.T) {
	api := &fakeSlackAPI{startErr: errSlackTest}
	sender := NewSender(api)

	err := sender.StreamTurn(context.Background(), slackTestTarget(), func(onDelta func(string)) (string, error) {
		return "", errSlackTest
	})

	require.ErrorIs(t, err, errSlackTest)
	require.Len(t, api.allCalls(), 1)
}

func TestSender_StreamTurnChunksLargeDeltasWithoutTrimming(t *testing.T) {
	api := &fakeSlackAPI{}
	sender := NewSender(api)
	delta := strings.Repeat("a", pkgslack.MarkdownTextLimit) + " b"

	err := sender.StreamTurn(context.Background(), slackTestTarget(), func(onDelta func(string)) (string, error) {
		onDelta(delta)
		return "final", nil
	})

	require.NoError(t, err)
	calls := api.allCalls()
	require.Len(t, calls, 4)
	require.Equal(t, strings.Repeat("a", pkgslack.MarkdownTextLimit), calls[1].text)
	require.Equal(t, " b", calls[2].text)
}

func TestSender_StreamTurnFallsBackWhenAppendFailsBeforeVisibleOutput(t *testing.T) {
	api := &fakeSlackAPI{appendErr: errSlackTest}
	sender := NewSender(api)
	target := slackTestTarget()

	err := sender.StreamTurn(context.Background(), target, func(onDelta func(string)) (string, error) {
		onDelta("first")
		onDelta("ignored")
		return "final", nil
	})

	require.NoError(t, err)
	require.Equal(t, []slackAPICall{
		{method: "startStream", target: target},
		{method: "appendStream", stream: pkgslack.Stream{ChannelID: "C1", TS: "stream-ts"}, text: "firstignored"},
		{method: "postMessage", target: target, text: "final"},
	}, api.allCalls())
}

func TestSender_StreamTurnTreatsTerminalAppendErrorBeforeVisibleAsHandled(t *testing.T) {
	api := &fakeSlackAPI{appendErr: slackAPIError{Code: "stopped_by_user"}}
	sender := NewSender(api)
	target := slackTestTarget()

	err := sender.StreamTurn(context.Background(), target, func(onDelta func(string)) (string, error) {
		onDelta("first")
		return "final", nil
	})

	require.NoError(t, err)
	require.Equal(t, []slackAPICall{
		{method: "startStream", target: target},
		{method: "appendStream", stream: pkgslack.Stream{ChannelID: "C1", TS: "stream-ts"}, text: "first"},
	}, api.allCalls())
}

func TestSender_StreamTurnDoesNotFallbackWhenAppendFailsAfterVisibleOutput(t *testing.T) {
	api := &fakeSlackAPI{appendErrAfter: 1}
	sender := NewSender(api)
	delta := strings.Repeat("a", pkgslack.MarkdownTextLimit) + " b"

	err := sender.StreamTurn(context.Background(), slackTestTarget(), func(onDelta func(string)) (string, error) {
		onDelta(delta)
		return "final", nil
	})

	require.NoError(t, err)
	require.Equal(t, []string{"startStream", "appendStream", "appendStream", "stopStream"}, api.callMethods())
}

func TestSender_StreamTurnSuppressesTerminalAppendErrorAfterVisibleOutput(t *testing.T) {
	api := &fakeSlackAPI{
		appendErrAfter:    1,
		appendErrAfterErr: slackAPIError{Code: "message_not_in_streaming_state"},
	}
	sender := NewSender(api)
	delta := strings.Repeat("a", pkgslack.MarkdownTextLimit) + " b"

	err := sender.StreamTurn(context.Background(), slackTestTarget(), func(onDelta func(string)) (string, error) {
		onDelta(delta)
		return "final", nil
	})

	require.NoError(t, err)
	require.Equal(t, []string{"startStream", "appendStream", "appendStream", "stopStream"}, api.callMethods())
}

func TestSender_StreamTurnReturnsStopErrorAfterVisibleOutput(t *testing.T) {
	api := &fakeSlackAPI{stopErr: errSlackTest}
	sender := NewSender(api)

	err := sender.StreamTurn(context.Background(), slackTestTarget(), func(onDelta func(string)) (string, error) {
		onDelta("visible")
		return "final", nil
	})

	require.ErrorIs(t, err, errSlackTest)
}

func TestSender_StreamTurnTreatsTerminalStopErrorAfterVisibleAsHandled(t *testing.T) {
	api := &fakeSlackAPI{stopErr: slackAPIError{Code: "streaming_state_conflict"}}
	sender := NewSender(api)

	err := sender.StreamTurn(context.Background(), slackTestTarget(), func(onDelta func(string)) (string, error) {
		onDelta("visible")
		return "final", nil
	})

	require.NoError(t, err)
	require.Equal(t, []string{"startStream", "appendStream", "stopStream"}, api.callMethods())
}

func TestSender_StreamTurnTreatsRateLimitStopErrorAfterVisibleAsHandled(t *testing.T) {
	api := &fakeSlackAPI{stopErr: slackAPIError{Code: "rate_limited"}}
	sender := NewSender(api)

	err := sender.StreamTurn(context.Background(), slackTestTarget(), func(onDelta func(string)) (string, error) {
		onDelta("visible")
		return "final", nil
	})

	require.NoError(t, err)
	require.Equal(t, []string{"startStream", "appendStream", "stopStream"}, api.callMethods())
}

func TestSender_StreamTurnFallsBackWhenStopFailsBeforeVisibleOutput(t *testing.T) {
	api := &fakeSlackAPI{stopErr: errSlackTest}
	sender := NewSender(api)

	err := sender.StreamTurn(context.Background(), slackTestTarget(), func(onDelta func(string)) (string, error) {
		onDelta(" ")
		return "final", nil
	})

	require.NoError(t, err)
	require.Equal(t, "postMessage", api.allCalls()[2].method)
}

func TestSender_StreamTurnTreatsTerminalStopErrorBeforeVisibleAsHandled(t *testing.T) {
	api := &fakeSlackAPI{stopErr: slackAPIError{Code: "stopped_by_user"}}
	sender := NewSender(api)

	err := sender.StreamTurn(context.Background(), slackTestTarget(), func(onDelta func(string)) (string, error) {
		onDelta(" ")
		return "final", nil
	})

	require.NoError(t, err)
	require.Equal(t, []string{"startStream", "stopStream"}, api.callMethods())
}

func TestSender_StreamTurnReturnsRunError(t *testing.T) {
	api := &fakeSlackAPI{}
	sender := NewSender(api)

	err := sender.StreamTurn(context.Background(), slackTestTarget(), func(onDelta func(string)) (string, error) {
		onDelta("visible")
		return "", errSlackTest
	})

	require.ErrorIs(t, err, errSlackTest)
}

func TestSender_SendFinalChunksLongText(t *testing.T) {
	api := &fakeSlackAPI{}
	sender := NewSender(api)
	target := slackTestTarget()
	text := strings.Repeat("a", pkgslack.MarkdownTextLimit+1)

	err := sender.SendFinal(context.Background(), target, text)

	require.NoError(t, err)
	calls := api.allCalls()
	require.Len(t, calls, 2)
	require.Equal(t, pkgslack.MarkdownTextLimit, len(calls[0].text))
	require.Equal(t, 1, len(calls[1].text))
}

func TestSendFinal_UsesConfiguredSlackClient(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := SendFinal(
		ctx,
		config.GatewaySlackConfig{BotToken: "slack-token"},
		pkgslack.Target{ChannelID: "C123"},
		"message",
	)

	require.ErrorIs(t, err, context.Canceled)
}

func TestSender_SendFinalFormatsSlackMrkdwn(t *testing.T) {
	api := &fakeSlackAPI{}
	sender := NewSender(api)
	target := slackTestTarget()

	err := sender.SendFinal(context.Background(), target, "**bold** and [Morph](https://example.com)")

	require.NoError(t, err)
	require.Equal(t, []slackAPICall{
		{method: "postMessage", target: target, text: "*bold* and <https://example.com|Morph>"},
	}, api.allCalls())
}

func TestSender_SendFinalReturnsPostError(t *testing.T) {
	api := &fakeSlackAPI{postErr: errSlackTest}

	err := NewSender(api).SendFinal(context.Background(), slackTestTarget(), "hello")

	require.ErrorIs(t, err, errSlackTest)
}

func TestSender_SendFinalSkipsEmptyText(t *testing.T) {
	api := &fakeSlackAPI{}

	err := NewSender(api).SendFinal(context.Background(), slackTestTarget(), " ")

	require.NoError(t, err)
	require.Empty(t, api.allCalls())
}

func TestSender_RequiresAPI(t *testing.T) {
	err := (*Sender)(nil).SendFinal(context.Background(), slackTestTarget(), "hello")
	require.EqualError(t, err, "slack sender is required")

	err = NewSender(nil).StreamTurn(context.Background(), slackTestTarget(), func(func(string)) (string, error) {
		return "", nil
	})
	require.EqualError(t, err, "slack sender is required")
}

func TestSlackStreamAppender_DefaultsInvalidInterval(t *testing.T) {
	appender := newSlackStreamAppender(context.Background(), &fakeSlackAPI{}, pkgslack.Stream{}, 0)

	require.Equal(t, defaultSlackStreamFlushInterval, appender.interval)
}

func TestSlackStreamAppender_FlushesOnTicker(t *testing.T) {
	api := &fakeSlackAPI{}
	appender := newSlackStreamAppender(
		context.Background(),
		api,
		pkgslack.Stream{ChannelID: "C1", TS: "stream-ts"},
		time.Millisecond,
	)
	appender.Start()
	appender.Append("hello\n\n")

	require.Eventually(t, func() bool {
		return len(api.allCalls()) == 1
	}, time.Second, 10*time.Millisecond)

	state, err := appender.Stop()
	require.NoError(t, err)
	require.True(t, state.visible)
	require.Equal(t, []slackAPICall{
		{method: "appendStream", stream: pkgslack.Stream{ChannelID: "C1", TS: "stream-ts"}, text: "hello\n\n"},
	}, api.allCalls())
}

func TestSlackStreamAppender_FlushesOnContextCancellation(t *testing.T) {
	api := &fakeSlackAPI{}
	ctx, cancel := context.WithCancel(context.Background())
	appender := newSlackStreamAppender(
		ctx,
		api,
		pkgslack.Stream{ChannelID: "C1", TS: "stream-ts"},
		time.Hour,
	)
	appender.Start()
	appender.Append("hello\n")
	cancel()

	require.Eventually(t, func() bool {
		return len(api.allCalls()) == 1
	}, time.Second, 10*time.Millisecond)

	state, err := appender.Stop()
	require.NoError(t, err)
	require.True(t, state.visible)
}

func TestSlackStreamAppender_SkipsBlankAndAfterError(t *testing.T) {
	api := &fakeSlackAPI{appendErr: errSlackTest}
	appender := newSlackStreamAppender(
		context.Background(),
		api,
		pkgslack.Stream{ChannelID: "C1", TS: "stream-ts"},
		time.Hour,
	)
	appender.Start()
	appender.Append("")
	appender.Append("first")
	appender.Append("ignored")

	state, err := appender.Stop()

	require.ErrorIs(t, err, errSlackTest)
	require.False(t, state.visible)
	appender.Append("ignored")
	require.Equal(t, []slackAPICall{
		{method: "appendStream", stream: pkgslack.Stream{ChannelID: "C1", TS: "stream-ts"}, text: "firstignored"},
	}, api.allCalls())
}

func TestSlackStreamAppender_StreamsFencedCodeAsMarkdownText(t *testing.T) {
	api := &fakeSlackAPI{}
	appender := newSlackStreamAppender(
		context.Background(),
		api,
		pkgslack.Stream{ChannelID: "C1", TS: "stream-ts"},
		time.Hour,
	)
	appender.Start()
	appender.Append("```go\n")
	appender.Append("fmt.Println(1)\n")
	appender.Append("```\n")

	state, err := appender.Stop()

	require.NoError(t, err)
	require.True(t, state.visible)
	require.Equal(t, []slackAPICall{
		{
			method: "appendStream",
			stream: pkgslack.Stream{ChannelID: "C1", TS: "stream-ts"},
			text:   "```\nfmt.Println(1)\n```",
		},
		{
			method: "appendStream",
			stream: pkgslack.Stream{ChannelID: "C1", TS: "stream-ts"},
			text:   "\n",
		},
	}, api.allCalls())
}

func TestSlackStreamAppender_PreservesWhitespaceOnlyDeltasInFencedCode(t *testing.T) {
	api := &fakeSlackAPI{}
	appender := newSlackStreamAppender(
		context.Background(),
		api,
		pkgslack.Stream{ChannelID: "C1", TS: "stream-ts"},
		time.Hour,
	)
	appender.Start()
	appender.Append("```go\npackage main")
	appender.Append("\n")
	appender.Append("import \"fmt\"\n")
	appender.Append("```")

	state, err := appender.Stop()

	require.NoError(t, err)
	require.True(t, state.visible)
	require.Equal(t, []slackAPICall{
		{
			method: "appendStream",
			stream: pkgslack.Stream{ChannelID: "C1", TS: "stream-ts"},
			text:   "```\npackage main\nimport \"fmt\"\n```",
		},
	}, api.allCalls())
}

func TestGetSlackStreamChunksSkipsBlankText(t *testing.T) {
	require.Nil(t, getSlackStreamChunks("   "))
}

func TestGetSlackStreamChunksSplitsLongText(t *testing.T) {
	text := strings.Repeat("a", pkgslack.MarkdownTextLimit) + " b"

	chunks := getSlackStreamChunks(text)

	require.Equal(t, []pkgslack.Chunk{
		pkgslack.MarkdownTextChunk(strings.Repeat("a", pkgslack.MarkdownTextLimit)),
		pkgslack.MarkdownTextChunk(" b"),
	}, chunks)
}

func TestSlackStreamAppender_AppendAfterErrorSkipsPendingChunks(t *testing.T) {
	appender := newSlackStreamAppender(
		context.Background(),
		&fakeSlackAPI{},
		pkgslack.Stream{ChannelID: "C1", TS: "stream-ts"},
		time.Hour,
	)
	appender.err = errSlackTest

	appender.appendChunks([]pkgslack.Chunk{pkgslack.MarkdownTextChunk("ignored")})

	require.Empty(t, appender.pending)
}

func TestSlackStreamErrorClassification(t *testing.T) {
	require.False(t, isSlackStreamNonRetryableAfterVisible(nil))
	require.False(t, isSlackStreamNonRetryableAfterVisible(errSlackTest))
	require.True(t, isSlackStreamNonRetryableAfterVisible(slackAPIError{Code: "http_status_503"}))
	require.Empty(t, getSlackAPIErrorCode(nil))
}

func TestGetSlackStreamSafeFormatIndexWaitsForCompleteLinesAndFences(t *testing.T) {
	require.Zero(t, getSlackStreamSafeFormatIndex("hello", false))
	require.Zero(t, getSlackStreamSafeFormatIndex("hello\nnext", false))
	require.Equal(t, len("hello\n\n"), getSlackStreamSafeFormatIndex("hello\n\nnext", false))
	require.Zero(t, getSlackStreamSafeFormatIndex("```go\nfmt.Println(1)\n", false))

	closedFence := "```go\nfmt.Println(1)\n```\n"
	require.Zero(t, getSlackStreamSafeFormatIndex(closedFence, false))
	require.Equal(t, len(closedFence), getSlackStreamSafeFormatIndex(closedFence+"next\n", false))
	require.Equal(t, len(closedFence), getSlackStreamSafeFormatIndex(closedFence+"next", false))
	require.Equal(t, len("hello"), getSlackStreamSafeFormatIndex("hello", true))
}

func slackTestTarget() pkgslack.Target {
	return pkgslack.Target{
		TeamID:          "T1",
		ChannelID:       "C1",
		ThreadTS:        "100.1",
		UserID:          "U1",
		ChannelType:     "channel",
		RecipientUserID: "U1",
		RecipientTeamID: "T1",
	}
}
