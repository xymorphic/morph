package automation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/xymorphic/morph/pkg/str"
)

const (
	metadataOriginChannel = "origin_channel"
	metadataOriginTarget  = "origin_target"
	metadataOriginThread  = "origin_thread_id"
)

type DeliveryTarget struct {
	Mode     DeliveryMode `json:"mode"`
	Channel  string       `json:"channel"`
	Target   string       `json:"target"`
	ThreadID string       `json:"threadId"`
}

type DeliveryRequest struct {
	JobID     string         `json:"jobId"`
	RunID     string         `json:"runId"`
	Status    RunStatus      `json:"status"`
	Output    string         `json:"output"`
	Error     string         `json:"error"`
	SessionID string         `json:"sessionId"`
	Target    DeliveryTarget `json:"target"`
}

type DeliveryResult struct {
	Status              DeliveryStatus
	Error               string
	FailureNoticeSentAt time.Time
}

type DeliverySink interface {
	DeliverAutomation(context.Context, DeliveryRequest) error
}

type DeliverySinkFunc func(context.Context, DeliveryRequest) error

func (fn DeliverySinkFunc) DeliverAutomation(ctx context.Context, req DeliveryRequest) error {
	if fn == nil {
		return errors.New("automation delivery sink is required")
	}

	return fn(ctx, req)
}

type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

func normalizeDelivery(delivery Delivery) Delivery {
	mode := str.String(delivery.Mode)
	channel := str.String(delivery.Channel)
	target := str.String(delivery.Target)
	threadID := str.String(delivery.ThreadID)
	webhookURL := str.String(delivery.WebhookURL)
	failureTarget := str.String(delivery.FailureTarget)

	delivery.Mode = DeliveryMode(mode.Normalized())
	if delivery.Mode == "" {
		delivery.Mode = DeliveryNone
	}
	delivery.Channel = channel.Normalized()
	delivery.Target = target.Trim()
	delivery.ThreadID = threadID.Trim()
	delivery.WebhookURL = webhookURL.Trim()
	delivery.FailureTarget = failureTarget.Trim()
	if delivery.FailureAfter < 0 {
		delivery.FailureAfter = 0
	}
	if delivery.FailureCooldown < 0 {
		delivery.FailureCooldown = 0
	}

	return delivery
}

func getDeliveryTarget(job Job, delivery Delivery) DeliveryTarget {
	target := DeliveryTarget{
		Mode:     delivery.Mode,
		Channel:  delivery.Channel,
		Target:   delivery.Target,
		ThreadID: delivery.ThreadID,
	}
	if target.Mode != DeliveryOrigin {
		return target
	}

	if target.Channel == "" {
		channel := str.String(job.Payload.Metadata[metadataOriginChannel])
		target.Channel = channel.Normalized()
	}
	if target.Target == "" {
		targetValue := str.String(job.Payload.Metadata[metadataOriginTarget])
		target.Target = targetValue.Trim()
	}
	if target.ThreadID == "" {
		threadValue := str.String(job.Payload.Metadata[metadataOriginThread])
		target.ThreadID = threadValue.Trim()
	}

	return target
}

func getFailureDeliveryTarget(job Job, delivery Delivery) DeliveryTarget {
	target := getDeliveryTarget(job, delivery)
	failureTarget := str.String(delivery.FailureTarget)
	if value := failureTarget.Trim(); value != "" {
		target.Target = value
	}

	return target
}

func checkFailureNoticeDue(job Job, delivery Delivery, now time.Time) bool {
	if delivery.FailureAfter <= 0 {
		return false
	}

	nextErrorCount := job.State.ConsecutiveErrors + 1
	if nextErrorCount < delivery.FailureAfter {
		return false
	}
	if delivery.FailureCooldown <= 0 || job.State.LastFailureNoticeAt.IsZero() {
		return true
	}

	return !job.State.LastFailureNoticeAt.Add(delivery.FailureCooldown).After(now)
}

func newDeliveryRequest(
	job Job,
	runID string,
	status RunStatus,
	result RunResult,
	target DeliveryTarget,
	runErr error,
) DeliveryRequest {
	req := DeliveryRequest{
		JobID:     job.ID,
		RunID:     runID,
		Status:    status,
		Output:    result.Output,
		SessionID: result.SessionID,
		Target:    target,
	}
	if runErr != nil {
		req.Error = runErr.Error()
	}

	return req
}

func deliverWebhook(ctx context.Context, client HTTPClient, url string, req DeliveryRequest) error {
	if client == nil {
		client = http.DefaultClient
	}
	webhookURL := str.String(url)
	trimmedURL := webhookURL.Trim()
	if trimmedURL == "" {
		return errors.New("automation webhook URL is required")
	}

	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, trimmedURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(httpReq)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	message := str.String(string(responseBody))
	if text := message.Trim(); text != "" {
		return fmt.Errorf("automation webhook delivery failed: %s: %s", resp.Status, text)
	}

	return fmt.Errorf("automation webhook delivery failed: %s", resp.Status)
}
