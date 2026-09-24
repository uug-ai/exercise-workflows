package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"
	log "github.com/sirupsen/logrus"
)

func TestLogInvocationIncludesRedactedMessage(t *testing.T) {
	logger := log.New()
	var output bytes.Buffer
	logger.SetOutput(&output)
	logger.SetFormatter(&log.JSONFormatter{})

	logInvocation(logger, amqp.Delivery{
		Body:        []byte(sampleInvocation),
		DeliveryTag: 42,
		Redelivered: true,
		RoutingKey:  "exercise",
	})

	var entry struct {
		Event      string         `json:"event"`
		Invocation map[string]any `json:"invocation"`
	}
	if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	callback, ok := entry.Invocation["callback"].(map[string]any)
	if entry.Event != "workflow_invocation_received" || !ok || callback["url"] != "https://api.example.com/workflows/runs/665f1b2c3d4e5f6071829304" {
		t.Fatalf("unexpected invocation log: %s", output.String())
	}
	encoded, err := json.Marshal(entry.Invocation)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "SECRET") || strings.Contains(string(encoded), "secret=SECRET") || !strings.Contains(string(encoded), redactionMarker) {
		t.Fatalf("invocation log was not redacted: %s", encoded)
	}
}

func TestLogInvocationDoesNotLogMalformedBody(t *testing.T) {
	logger := log.New()
	var output bytes.Buffer
	logger.SetOutput(&output)
	logger.SetFormatter(&log.JSONFormatter{})

	logInvocation(logger, amqp.Delivery{Body: []byte(`{"token":"SECRET"`)})

	if strings.Contains(output.String(), "SECRET") || !strings.Contains(output.String(), "Received malformed workflow invocation") {
		t.Fatalf("malformed invocation was logged unsafely: %s", output.String())
	}
}

func TestLogInvocationDoesNotLogNonObjectBody(t *testing.T) {
	logger := log.New()
	var output bytes.Buffer
	logger.SetOutput(&output)
	logger.SetFormatter(&log.JSONFormatter{})

	logInvocation(logger, amqp.Delivery{Body: []byte(`"SECRET"`)})

	if strings.Contains(output.String(), "SECRET") || !strings.Contains(output.String(), "Received malformed workflow invocation") {
		t.Fatalf("non-object invocation was logged unsafely: %s", output.String())
	}
}

func TestRedactURLCredentials(t *testing.T) {
	got := redactURLCredentials("https://user:password@example.com/path?signature=SECRET#fragment")
	if got != "https://example.com/path?"+redactionMarker {
		t.Fatalf("redactURLCredentials() = %q", got)
	}
}
