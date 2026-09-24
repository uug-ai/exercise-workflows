package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	log "github.com/sirupsen/logrus"
)

const redactionMarker = "[REDACTED]"

func configureLogging(levelValue, outputValue string) {
	logger := log.StandardLogger()
	level, levelErr := log.ParseLevel(strings.ToLower(strings.TrimSpace(levelValue)))
	if levelErr != nil {
		level = log.InfoLevel
	}
	output := strings.ToLower(strings.TrimSpace(outputValue))
	var outputErr error
	if output != "json" && output != "text" {
		outputErr = fmt.Errorf("unsupported LOG_OUTPUT %q", outputValue)
		output = "text"
	}

	callerPrettyfier := func(frame *runtime.Frame) (string, string) {
		return filepath.Base(frame.Function), fmt.Sprintf("%s:%d", filepath.Base(frame.File), frame.Line)
	}
	logger.SetOutput(os.Stdout)
	logger.SetLevel(level)
	logger.SetReportCaller(true)
	if output == "json" {
		logger.SetFormatter(&log.JSONFormatter{
			CallerPrettyfier: callerPrettyfier,
			TimestampFormat:  time.RFC3339Nano,
		})
	} else {
		logger.SetFormatter(&log.TextFormatter{
			CallerPrettyfier: callerPrettyfier,
			FullTimestamp:    true,
			TimestampFormat:  time.RFC3339Nano,
		})
	}
	if levelErr != nil {
		logger.WithFields(log.Fields{
			"component":        "workflow_stage",
			"configured_level": levelValue,
			"effective_level":  level.String(),
			"event":            "log_level_invalid",
		}).WithError(levelErr).Warn("Invalid log level; using default")
	}
	if outputErr != nil {
		logger.WithFields(log.Fields{
			"component":         "workflow_stage",
			"configured_output": outputValue,
			"effective_output":  output,
			"event":             "log_output_invalid",
		}).WithError(outputErr).Warn("Invalid log output; using default")
	}
	logger.WithFields(log.Fields{
		"component":     "workflow_stage",
		"event":         "logger_configured",
		"log_level":     level.String(),
		"output":        output,
		"report_caller": logger.ReportCaller,
	}).Debug("Logging configured")
}

func logInvocation(logger *log.Logger, delivery amqp.Delivery) {
	invocation, err := redactedInvocation(delivery.Body)
	fields := log.Fields{
		"body_bytes":   len(delivery.Body),
		"component":    "workflow_stage",
		"delivery_tag": delivery.DeliveryTag,
		"event":        "workflow_invocation_received",
		"redelivered":  delivery.Redelivered,
		"routing_key":  delivery.RoutingKey,
	}
	if err != nil {
		logger.WithError(err).WithFields(fields).Warn("Received malformed workflow invocation")
		return
	}
	fields["invocation"] = invocation
	logger.WithFields(fields).Info("Workflow invocation received")
}

func redactedInvocation(body []byte) (any, error) {
	var invocation map[string]any
	if err := json.Unmarshal(body, &invocation); err != nil {
		return nil, err
	}
	if invocation == nil {
		return nil, fmt.Errorf("expected a workflow invocation JSON object")
	}
	return redactValue(invocation, ""), nil
}

func redactValue(value any, key string) any {
	if isSensitiveKey(key) {
		return redactionMarker
	}
	switch typed := value.(type) {
	case map[string]any:
		redacted := make(map[string]any, len(typed))
		for childKey, childValue := range typed {
			redacted[childKey] = redactValue(childValue, childKey)
		}
		return redacted
	case []any:
		redacted := make([]any, len(typed))
		for index, childValue := range typed {
			redacted[index] = redactValue(childValue, key)
		}
		return redacted
	case string:
		return redactURLCredentials(typed)
	default:
		return value
	}
}

func isSensitiveKey(key string) bool {
	normalized := strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(key))
	for _, fragment := range []string{
		"accesskey", "apikey", "authorization", "cookie", "credential", "passphrase",
		"password", "privatekey", "secret", "signature", "signedurl", "storage", "token",
	} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}

func redactURLCredentials(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" || parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == "" {
		return value
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String() + "?" + redactionMarker
}
