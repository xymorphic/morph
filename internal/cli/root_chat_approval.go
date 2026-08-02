package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/muesli/reflow/wordwrap"

	"github.com/xymorphic/morph/internal/permissions"
	rpcclient "github.com/xymorphic/morph/internal/rpc/client"
	"github.com/xymorphic/morph/internal/rpc/rpcmeta"
	"github.com/xymorphic/morph/internal/trace"
	"github.com/xymorphic/morph/pkg/str"
)

type rootChatPermissionAPIProvider interface {
	PermissionAPI() rpcclient.PermissionAPI
}

func getRootChatPermissionAPI(client rpcclient.ChatClient) rpcclient.PermissionAPI {
	provider, ok := client.(rootChatPermissionAPIProvider)
	if !ok {
		return nil
	}

	return provider.PermissionAPI()
}

type rootChatApprovalHandler struct {
	input       *bufio.Reader
	output      io.Writer
	permissions rpcclient.PermissionAPI
	interactive bool
	now         func() time.Time
	width       int
}

func newRootChatApprovalHandler(
	input io.Reader,
	output io.Writer,
	permissionAPI rpcclient.PermissionAPI,
	interactive bool,
) *rootChatApprovalHandler {
	var reader *bufio.Reader
	if input != nil {
		reader = bufio.NewReader(input)
	}

	return &rootChatApprovalHandler{
		input:       reader,
		output:      output,
		permissions: permissionAPI,
		interactive: interactive,
		now:         time.Now,
		width:       getRootChatApprovalWidth(output),
	}
}

func (h *rootChatApprovalHandler) Handle(ctx context.Context, event rpcclient.Event) (bool, error) {
	traceEvent, ok := event.TraceEvent.(*trace.Event)
	if !ok || traceEvent == nil || traceEvent.Type != trace.EvtPermissionApprovalChanged {
		return false, nil
	}

	decoded, ok := trace.DecodePayload(traceEvent.Type, traceEvent.Payload)
	if !ok {
		return true, errors.New("invalid permission approval event")
	}
	payload, ok := decoded.(trace.PermissionApprovalPayload)
	if !ok || str.String(payload.RequestID).Trim() == "" {
		return true, errors.New("invalid permission approval event")
	}
	return true, h.handlePayload(ctx, payload)
}

func (h *rootChatApprovalHandler) HandleRequest(
	ctx context.Context,
	request permissions.ApprovalRequest,
) error {
	effects := make([]string, len(request.Effects))
	for index, effect := range request.Effects {
		effects[index] = string(effect)
	}
	return h.handlePayload(ctx, trace.PermissionApprovalPayload{
		RequestID:  request.ID,
		Status:     string(request.Status),
		Scope:      string(request.Scope),
		Tool:       request.Tool,
		Resource:   string(request.Resource),
		Action:     string(request.Action),
		Effects:    effects,
		Summary:    request.Summary,
		Reason:     request.Reason,
		Operations: request.Operations,
		ExpiresAt:  request.ExpiresAt,
	})
}

func (h *rootChatApprovalHandler) handlePayload(
	ctx context.Context,
	payload trace.PermissionApprovalPayload,
) error {
	if payload.Status != string(permissions.ApprovalPending) {
		return nil
	}
	if !h.interactive {
		return fmt.Errorf(
			"approval required for %s; root chat input and output must be an interactive terminal (%s)",
			payload.Summary,
			payload.RequestID,
		)
	}
	if h.input == nil || h.permissions == nil {
		return errors.New("interactive permission approval is unavailable")
	}

	approved, scope, err := h.prompt(ctx, payload)
	if err != nil {
		return err
	}
	resolveCtx := rpcmeta.WithOutgoingPermissionSurface(ctx, permissions.SurfaceCLI)
	request, err := h.permissions.ResolveApprovalRequest(resolveCtx, payload.RequestID, approved, scope)
	if err != nil {
		return fmt.Errorf("resolve permission approval: %w", err)
	}

	status := "denied"
	if approved {
		status = "approved (" + string(scope) + ")"
	}
	summary := request.Summary
	if str.String(summary).Trim() == "" {
		summary = payload.Summary
	}
	_, err = fmt.Fprintf(h.output, "\nPermission %s — %s\n\n", status, summary)

	return err
}

func (h *rootChatApprovalHandler) prompt(
	ctx context.Context,
	payload trace.PermissionApprovalPayload,
) (bool, permissions.GrantScope, error) {
	prompt := permissions.GetApprovalPrompt(
		payload.Summary,
		payload.Effects,
		payload.Reason,
		payload.Operations,
		payload.ExpiresAt,
	)
	if _, err := fmt.Fprintln(h.output, "\nPermission approval required"); err != nil {
		return false, "", err
	}
	if err := writeRootChatApprovalField(h.output, "Operation", prompt.Summary, h.width); err != nil {
		return false, "", err
	}
	if len(prompt.Effects) > 0 {
		if err := writeRootChatApprovalField(
			h.output,
			"Effects",
			strings.Join(prompt.Effects, ", "),
			h.width,
		); err != nil {
			return false, "", err
		}
	}
	if prompt.Reason != "" {
		if err := writeRootChatApprovalField(h.output, "Reason", prompt.Reason, h.width); err != nil {
			return false, "", err
		}
	}
	if len(prompt.Operations) > 0 {
		if _, err := fmt.Fprintln(h.output, "  Operations"); err != nil {
			return false, "", err
		}
		for index, operation := range prompt.Operations {
			if err := writeRootChatApprovalOperation(h.output, index+1, operation, h.width); err != nil {
				return false, "", err
			}
		}
	}
	if expiry := permissions.GetApprovalExpiryText(prompt.ExpiresAt, h.now()); expiry != "" {
		if err := writeRootChatApprovalField(h.output, "Expires", expiry, h.width); err != nil {
			return false, "", err
		}
	}

	choices := permissions.GetApprovalChoices(prompt.Effects)
	if _, err := fmt.Fprintln(h.output); err != nil {
		return false, "", err
	}
	for _, choice := range choices {
		if _, err := fmt.Fprintf(h.output, "[%c] %-18s %s\n", choice.Key, choice.Label, choice.Detail); err != nil {
			return false, "", err
		}
	}
	if _, err := fmt.Fprint(h.output, "> "); err != nil {
		return false, "", err
	}

	result := make(chan rootChatApprovalChoice, 1)
	go func() {
		approved, scope, err := h.readChoice(choices)
		result <- rootChatApprovalChoice{approved: approved, scope: scope, err: err}
	}()

	var expiry <-chan time.Time
	var timer *time.Timer
	if !payload.ExpiresAt.IsZero() {
		timer = time.NewTimer(max(time.Until(payload.ExpiresAt), 0))
		expiry = timer.C
		defer timer.Stop()
	}

	select {
	case choice := <-result:
		return choice.approved, choice.scope, choice.err
	case <-ctx.Done():
		return false, "", ctx.Err()
	case <-expiry:
		return false, "", fmt.Errorf("permission approval %s expired", payload.RequestID)
	}
}

type rootChatApprovalChoice struct {
	approved bool
	scope    permissions.GrantScope
	err      error
}

func (h *rootChatApprovalHandler) readChoice(
	choices []permissions.ApprovalChoice,
) (bool, permissions.GrantScope, error) {
	for {
		value, readErr := h.input.ReadString('\n')
		if choice, ok := getRootChatApprovalChoice(choices, value); ok {
			return choice.Approved, choice.Scope, nil
		}
		if readErr != nil {
			return false, "", fmt.Errorf("read permission approval: %w", readErr)
		}
		if _, err := fmt.Fprintf(h.output, "Choose %s: ", getRootChatApprovalKeys(choices)); err != nil {
			return false, "", err
		}
	}
}

func writeRootChatApprovalField(output io.Writer, label string, value string, width int) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}

	prefix := fmt.Sprintf("  %-9s  ", label)
	return writeRootChatWrappedValue(output, prefix, value, width)
}

func writeRootChatApprovalOperation(output io.Writer, index int, value string, width int) error {
	return writeRootChatWrappedValue(output, fmt.Sprintf("    %d. ", index), value, width)
}

func writeRootChatWrappedValue(output io.Writer, prefix string, value string, width int) error {
	wrapWidth := max(width-len(prefix), 1)
	for index, line := range strings.Split(wordwrap.String(value, wrapWidth), "\n") {
		if index == 0 {
			if _, err := fmt.Fprintln(output, prefix+line); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintln(output, strings.Repeat(" ", len(prefix))+line); err != nil {
			return err
		}
	}

	return nil
}

func getRootChatApprovalChoice(
	choices []permissions.ApprovalChoice,
	value string,
) (permissions.ApprovalChoice, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "yes":
		value = "y"
	case "session":
		value = "s"
	case "always":
		value = "a"
	case "no", "deny":
		value = "n"
	default:
		value = strings.ToLower(strings.TrimSpace(value))
	}
	if len(value) != 1 {
		return permissions.ApprovalChoice{}, false
	}

	return permissions.GetApprovalChoiceByKey(choices, rune(value[0]))
}

func getRootChatApprovalKeys(choices []permissions.ApprovalChoice) string {
	keys := make([]string, len(choices))
	for index, choice := range choices {
		keys[index] = string(choice.Key)
	}

	return strings.Join(keys, ", ")
}

func getRootChatApprovalWidth(output io.Writer) int {
	const fallbackWidth = 100

	file, ok := output.(interface{ Fd() uintptr })
	if !ok {
		return fallbackWidth
	}
	width, _, err := term.GetSize(file.Fd())
	if err != nil || width <= 0 {
		return fallbackWidth
	}

	return width
}
