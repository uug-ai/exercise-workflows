package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/uug-ai/models/pkg/models"
)

type config struct {
	operation, mode, token, brokerURL, queue string
	start, duration                          int64
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func loadConfig(offline bool) (config, error) {
	settings := config{
		operation: envOr("STAGE_OPERATION", operation),
		mode:      envOr("RESULT_MODE", "preview"),
		token:     os.Getenv("WORKFLOW_BEARER_TOKEN"),
		queue:     os.Getenv("STAGE_QUEUE"),
	}
	settings.start, _ = strconv.ParseInt(os.Getenv("MARKER_START_TIMESTAMP"), 10, 64)
	settings.duration, _ = strconv.ParseInt(envOr("MARKER_DURATION_SECONDS", "30"), 10, 64)
	if settings.start < 1 || settings.start > 253402300799-settings.duration || settings.duration < 1 || settings.duration > 3600 {
		return settings, errors.New("set MARKER_START_TIMESTAMP to the recording's Unix seconds and MARKER_DURATION_SECONDS to 1..3600")
	}
	if offline {
		return settings, nil
	}
	if settings.mode != "preview" && settings.mode != "bypass" && settings.mode != "http" {
		return settings, errors.New("RESULT_MODE must be preview, bypass or http")
	}
	if settings.mode == "http" {
		if strings.TrimSpace(settings.token) == "" || strings.ContainsAny(settings.token, "\r\n") {
			return settings, errors.New("set a valid WORKFLOW_BEARER_TOKEN")
		}
	}
	host := os.Getenv("RABBITMQ_HOST")
	if _, _, err := net.SplitHostPort(host); err != nil {
		return settings, errors.New("set RABBITMQ_HOST as hostname:port")
	}
	if settings.queue == "" || os.Getenv("RABBITMQ_USERNAME") == "" || os.Getenv("RABBITMQ_PASSWORD") == "" {
		return settings, errors.New("set STAGE_QUEUE, RABBITMQ_USERNAME and RABBITMQ_PASSWORD")
	}
	scheme := envOr("RABBITMQ_SCHEME", "amqps")
	if scheme != "amqp" && scheme != "amqps" {
		return settings, errors.New("RABBITMQ_SCHEME must be amqp or amqps")
	}
	vhost := envOr("RABBITMQ_VHOST", "/")
	broker := url.URL{
		Scheme: scheme, Host: host,
		User: url.UserPassword(os.Getenv("RABBITMQ_USERNAME"), os.Getenv("RABBITMQ_PASSWORD")),
		Path: "/" + vhost, RawPath: "/" + url.PathEscape(vhost),
	}
	settings.brokerURL = broker.String()
	return settings, nil
}

func validateCallback(callback *models.WorkflowCallback, runID string) error {
	if callback == nil || callback.URL == "" || callback.Method != models.WorkflowCallbackMethodPost || callback.ResultSchema != models.WorkflowResultSchemaV1 {
		return errors.New("callback must provide a URL, POST method and uug.ai/workflow-result/v1 result schema")
	}
	endpoint, err := url.Parse(callback.URL)
	if err != nil || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || !strings.Contains(endpoint.Path, "/workflows/") || !strings.HasSuffix(endpoint.Path, "/"+runID) {
		return errors.New("callback.url must be a credential-free workflow URL whose final path segment is runId")
	}
	loopback := endpoint.Hostname() == "localhost" || endpoint.Hostname() == "127.0.0.1" || endpoint.Hostname() == "::1"
	if endpoint.Scheme != "https" && !(endpoint.Scheme == "http" && loopback) {
		return errors.New("callback requires HTTPS (HTTP is allowed only on loopback for testing)")
	}
	return nil
}

func postResult(ctx context.Context, settings config, invocation handledInvocation) error {
	if !runIDPattern.MatchString(invocation.runID) {
		return errors.New("invalid callback runId")
	}
	if invocation.callback == nil || invocation.callback.URL == "" {
		return errors.New("cannot post result without callback.url")
	}
	body, err := json.Marshal(invocation.result)
	if err != nil {
		return errors.New("cannot encode callback body")
	}
	request, err := http.NewRequestWithContext(ctx, invocation.callback.Method, invocation.callback.URL, bytes.NewReader(body))
	if err != nil {
		return errors.New("cannot create callback request")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+settings.token)
	request.Header.Set("Idempotency-Key", invocation.runID+":"+settings.operation)
	log.WithFields(callbackLogFields(invocation, settings.operation, "workflow_result_submitting", body)).
		Info("Submitting workflow result")
	client := &http.Client{
		Timeout:       30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	response, err := client.Do(request)
	if err != nil {
		log.WithError(err).
			WithFields(callbackLogFields(invocation, settings.operation, "workflow_result_submission_failed", body)).
			Error("Workflow callback request failed")
		return fmt.Errorf("callback request to %s failed; check endpoint availability and TLS", invocation.callback.URL)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		log.WithFields(callbackLogFields(invocation, settings.operation, "workflow_result_rejected", body)).
			WithField("status_code", response.StatusCode).
			Error("Workflow callback rejected result")
		return fmt.Errorf("callback %s returned HTTP %d; message will remain available for replay", invocation.callback.URL, response.StatusCode)
	}
	log.WithFields(callbackLogFields(invocation, settings.operation, "workflow_result_submitted", body)).
		WithField("status_code", response.StatusCode).
		Info("Workflow result submitted")
	return nil
}

func callbackLogFields(invocation handledInvocation, operation, event string, resultPayload []byte) log.Fields {
	fields := log.Fields{
		"component":      "workflow_stage",
		"event":          event,
		"operation":      operation,
		"result_payload": string(resultPayload),
		"run_id":         invocation.runID,
	}
	if invocation.callback != nil {
		fields["callback_url"] = invocation.callback.URL
	}
	return fields
}

func printPreview(output io.Writer, invocation handledInvocation) error {
	return json.NewEncoder(output).Encode(struct {
		RunID string `json:"runId"`
		models.WorkflowResult
	}{
		RunID:          invocation.runID,
		WorkflowResult: invocation.result,
	})
}
