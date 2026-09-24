package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"
)

type acknowledgements struct {
	acks, nacks int
	requeue     bool
}

func (ack *acknowledgements) Ack(uint64, bool) error { ack.acks++; return nil }
func (ack *acknowledgements) Nack(_ uint64, _ bool, requeue bool) error {
	ack.nacks++
	ack.requeue = requeue
	return nil
}
func (ack *acknowledgements) Reject(uint64, bool) error { panic("unexpected reject") }

func TestDelivery(t *testing.T) {
	for _, test := range []struct {
		name, mode, body string
		status           int
		wantError        bool
		wantAck          bool
	}{
		{"success", "http", sampleInvocation, 204, false, true},
		{"unauthorized", "http", sampleInvocation, 401, true, false},
		{"unimplemented", "http", sampleInvocation, 404, true, false},
		{"server failure", "http", sampleInvocation, 500, true, false},
		{"redirect", "http", sampleInvocation, 302, true, false},
		{"preview", "preview", sampleInvocation, 0, false, false},
		{"bypass", "bypass", sampleInvocation, 0, false, true},
		{"invalid dispatch", "http", "{}", 0, true, false},
		{"invalid bypass dispatch", "bypass", "{}", 0, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				calls++
				if request.Method != "POST" || request.URL.Path != "/workflows/665f1b2c3d4e5f6071829304" || request.Header.Get("Authorization") != "Bearer test-token" || request.Header.Get("Content-Type") != "application/json" || request.Header.Get("Idempotency-Key") != "665f1b2c3d4e5f6071829304:forwarder" {
					t.Error("incorrect callback request")
				}
				body, _ := io.ReadAll(request.Body)
				var result map[string]json.RawMessage
				if json.Unmarshal(body, &result) != nil || result["payload"] == nil || strings.Contains(string(body), "SECRET") {
					t.Error("incorrect callback body")
				}
				writer.Header().Set("Location", "/must-not-follow")
				writer.WriteHeader(test.status)
			}))
			defer server.Close()
			body := strings.Replace(test.body, "https://api.example.com", server.URL, 1)
			settings := config{operation: "forwarder", mode: test.mode, token: "test-token", start: 1700000000, duration: 30}
			ack := &acknowledgements{}
			err := handleDelivery(context.Background(), settings, amqp.Delivery{Body: []byte(body), Acknowledger: ack}, io.Discard)
			if (err != nil) != test.wantError {
				t.Fatalf("error = %v", err)
			}
			if test.wantAck {
				if ack.acks != 1 || ack.nacks != 0 {
					t.Fatal("successful result was not acknowledged")
				}
			} else if ack.acks != 0 || ack.nacks != 1 || !ack.requeue {
				t.Fatal("message was not requeued")
			}
			if test.status == 0 && calls != 0 || test.status != 0 && calls != 1 {
				t.Fatalf("unexpected callback count: %d", calls)
			}
		})
	}
}

func TestCallbackValidation(t *testing.T) {
	const runID = "example-run"
	for _, endpoint := range []string{"", "https://example.com/workflows", "http://example.com/workflows/{runId}", "https://user:secret@example.com/workflows/{runId}", "https://example.com/workflows/{runId}?token=secret", "https://{runId}/workflows/example-run"} {
		if validateCallback(endpoint, runID) == nil {
			t.Errorf("accepted %q", endpoint)
		}
	}
	for _, endpoint := range []string{"https://example.com/api/workflows/example-run", "http://127.0.0.1:8080/workflows/example-run"} {
		if err := validateCallback(endpoint, runID); err != nil {
			t.Errorf("rejected %q: %v", endpoint, err)
		}
	}
}

func TestBrokerConfiguration(t *testing.T) {
	for name, value := range map[string]string{
		"RESULT_MODE": "preview", "MARKER_START_TIMESTAMP": "1700000000", "MARKER_DURATION_SECONDS": "30",
		"RABBITMQ_SCHEME": "amqps", "RABBITMQ_HOST": "localhost:5671", "RABBITMQ_VHOST": "team/one",
		"RABBITMQ_USERNAME": "user@team", "RABBITMQ_PASSWORD": "p@ss:/#word", "STAGE_QUEUE": "exercise",
	} {
		t.Setenv(name, value)
	}
	settings, err := loadConfig(false)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := amqp.ParseURI(settings.brokerURL)
	if err != nil || parsed.Username != "user@team" || parsed.Password != "p@ss:/#word" || parsed.Vhost != "team/one" {
		t.Fatal("broker configuration did not round-trip")
	}
	t.Setenv("RABBITMQ_VHOST", "/")
	settings, _ = loadConfig(false)
	parsed, err = amqp.ParseURI(settings.brokerURL)
	if err != nil || parsed.Vhost != "/" {
		t.Fatal("default vhost did not round-trip")
	}

	t.Setenv("RESULT_MODE", "bypass")
	t.Setenv("WORKFLOW_BEARER_TOKEN", "")
	if _, err := loadConfig(false); err != nil {
		t.Fatalf("bypass mode required callback configuration: %v", err)
	}
}
