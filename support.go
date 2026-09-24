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

func validateCallback(callbackURL, runID string) error {
	endpoint, err := url.Parse(callbackURL)
	if err != nil || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || !strings.HasSuffix(endpoint.Path, "/workflows/"+runID) {
		return errors.New("callbackUrl must end with /workflows/<runId>, without credentials, query or fragment")
	}
	loopback := endpoint.Hostname() == "localhost" || endpoint.Hostname() == "127.0.0.1" || endpoint.Hostname() == "::1"
	if endpoint.Scheme != "https" && !(endpoint.Scheme == "http" && loopback) {
		return errors.New("callback requires HTTPS (HTTP is allowed only on loopback for testing)")
	}
	return nil
}

func postResult(ctx context.Context, settings config, callbackURL string, result map[string]json.RawMessage) error {
	var runID string
	if json.Unmarshal(result["runId"], &runID) != nil || !runIDPattern.MatchString(runID) {
		return errors.New("invalid callback runId")
	}
	body, err := json.Marshal(result)
	if err != nil {
		return errors.New("cannot encode callback body")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, callbackURL, bytes.NewReader(body))
	if err != nil {
		return errors.New("cannot create callback request")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+settings.token)
	request.Header.Set("Idempotency-Key", runID+":"+settings.operation)
	client := &http.Client{
		Timeout:       30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	response, err := client.Do(request)
	if err != nil {
		return errors.New("callback request failed; check endpoint availability and TLS")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("callback returned HTTP %d; message will remain available for replay", response.StatusCode)
	}
	return nil
}

func printPreview(output io.Writer, result map[string]json.RawMessage) error {
	return json.NewEncoder(output).Encode(map[string]json.RawMessage{
		"runId": result["runId"], "operation": result["operation"], "payload": result["payload"],
	})
}
