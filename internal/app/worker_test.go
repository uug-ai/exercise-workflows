package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/uug-ai/models/pkg/models"
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
		wantOutcome      deliveryOutcome
	}{
		{"success", "http", sampleInvocation, 204, false, true, deliverySubmitted},
		{"unauthorized", "http", sampleInvocation, 401, true, false, deliverySubmitted},
		{"unimplemented", "http", sampleInvocation, 404, true, false, deliverySubmitted},
		{"server failure", "http", sampleInvocation, 500, true, false, deliverySubmitted},
		{"redirect", "http", sampleInvocation, 302, true, false, deliverySubmitted},
		{"missing callback", "http", strings.Replace(sampleInvocation, `"callback":{"url":"https://api.example.com/workflows/runs/665f1b2c3d4e5f6071829304","method":"POST","resultSchema":"uug.ai/workflow-result/v1"},`, "", 1), 0, false, true, deliveryCallbackMissing},
		{"missing callback URL", "http", strings.Replace(sampleInvocation, `"url":"https://api.example.com/workflows/runs/665f1b2c3d4e5f6071829304"`, `"url":""`, 1), 0, false, true, deliveryCallbackMissing},
		{"preview", "preview", sampleInvocation, 0, false, false, deliveryPreviewed},
		{"bypass", "bypass", sampleInvocation, 0, false, true, deliveryBypassed},
		{"invalid dispatch", "http", "{}", 0, true, false, deliverySubmitted},
		{"invalid bypass dispatch", "bypass", "{}", 0, true, false, deliverySubmitted},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				calls++
				if request.Method != "POST" || request.URL.Path != "/workflows/runs/665f1b2c3d4e5f6071829304" || request.Header.Get("Authorization") != "Bearer test-token" || request.Header.Get("Content-Type") != "application/json" || request.Header.Get("Idempotency-Key") != "665f1b2c3d4e5f6071829304:forwarder" {
					t.Error("incorrect callback request")
				}
				body, _ := io.ReadAll(request.Body)
				var result models.WorkflowResult
				if json.Unmarshal(body, &result) != nil || result.Schema != models.WorkflowResultSchemaV1 || result.Stage.Operation != "forwarder" || result.Result["blocks"] == nil || strings.Contains(string(body), "SECRET") {
					t.Error("incorrect callback body")
				}
				writer.Header().Set("Location", "/must-not-follow")
				writer.WriteHeader(test.status)
			}))
			defer server.Close()
			body := strings.Replace(test.body, "https://api.example.com", server.URL, 1)
			settings := config{operation: "forwarder", mode: test.mode, token: "test-token", start: 1700000000, duration: 30}
			ack := &acknowledgements{}
			outcome, err := handleDelivery(context.Background(), settings, amqp.Delivery{Body: []byte(body), Acknowledger: ack}, io.Discard)
			if (err != nil) != test.wantError {
				t.Fatalf("error = %v", err)
			}
			if test.status != 0 && test.wantError && !strings.Contains(err.Error(), server.URL+"/workflows/runs/665f1b2c3d4e5f6071829304") {
				t.Fatalf("callback error omitted URL: %v", err)
			}
			if outcome != test.wantOutcome {
				t.Fatalf("outcome = %v, want %v", outcome, test.wantOutcome)
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

func TestCallbackLogFieldsIncludeDestination(t *testing.T) {
	callback := models.WorkflowCallback{URL: "https://api.example.com/workflows/runs/example-run"}
	payload := []byte(`{"schema":"uug.ai/workflow-result/v1","stage":{"operation":"forwarder"},"result":{"blocks":[]}}`)
	fields := callbackLogFields(handledInvocation{runID: "example-run", callback: &callback}, "forwarder", "test_event", payload)
	if fields["callback_url"] != callback.URL || fields["run_id"] != "example-run" || fields["operation"] != "forwarder" || fields["event"] != "test_event" || fields["result_payload"] != string(payload) {
		t.Fatalf("unexpected callback log fields: %+v", fields)
	}
	var result models.WorkflowResult
	if err := json.Unmarshal([]byte(fields["result_payload"].(string)), &result); err != nil || result.Schema != models.WorkflowResultSchemaV1 {
		t.Fatalf("invalid logged result payload: %v, %+v", err, result)
	}
}

func TestMissingCallbackPrintsAndAcknowledgesResult(t *testing.T) {
	body := strings.Replace(sampleInvocation, `"callback":{"url":"https://api.example.com/workflows/runs/665f1b2c3d4e5f6071829304","method":"POST","resultSchema":"uug.ai/workflow-result/v1"},`, "", 1)
	settings := config{operation: "forwarder", mode: "http", token: "test-token", start: 1700000000, duration: 30}
	ack := &acknowledgements{}
	var output bytes.Buffer

	outcome, err := handleDelivery(context.Background(), settings, amqp.Delivery{Body: []byte(body), Acknowledger: ack}, &output)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != deliveryCallbackMissing || ack.acks != 1 || ack.nacks != 0 {
		t.Fatalf("outcome = %v, acknowledgements = %+v", outcome, ack)
	}
	var preview struct {
		RunID  string `json:"runId"`
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(output.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.RunID != "665f1b2c3d4e5f6071829304" || preview.Schema != models.WorkflowResultSchemaV1 {
		t.Fatalf("unexpected preview: %s", output.String())
	}
}

func TestCallbackValidation(t *testing.T) {
	const runID = "example-run"
	if validateCallback(nil, runID) == nil {
		t.Error("accepted nil callback")
	}
	for _, endpoint := range []string{"", "https://example.com/workflows", "http://example.com/workflows/{runId}", "https://user:secret@example.com/workflows/{runId}", "https://example.com/workflows/{runId}?token=secret", "https://{runId}/workflows/example-run"} {
		callback := models.WorkflowCallback{URL: endpoint, Method: models.WorkflowCallbackMethodPost, ResultSchema: models.WorkflowResultSchemaV1}
		if validateCallback(&callback, runID) == nil {
			t.Errorf("accepted %q", endpoint)
		}
	}
	for _, callback := range []models.WorkflowCallback{
		{URL: "https://example.com/workflows/runs/example-run", Method: "GET", ResultSchema: models.WorkflowResultSchemaV1},
		{URL: "https://example.com/workflows/runs/example-run", Method: models.WorkflowCallbackMethodPost, ResultSchema: "uug.ai/workflow-result/v2"},
	} {
		if validateCallback(&callback, runID) == nil {
			t.Errorf("accepted %+v", callback)
		}
	}
	for _, endpoint := range []string{"https://example.com/api/workflows/runs/example-run", "http://127.0.0.1:8080/workflows/runs/example-run"} {
		callback := models.WorkflowCallback{URL: endpoint, Method: models.WorkflowCallbackMethodPost, ResultSchema: models.WorkflowResultSchemaV1}
		if err := validateCallback(&callback, runID); err != nil {
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
