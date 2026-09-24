package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/uug-ai/models/pkg/models"
)

const sampleInvocation = `{"schema":"uug.ai/workflow-invocation/v1","executionId":"wfx_example","deliveryId":"wfx_example","runId":"665f1b2c3d4e5f6071829304","callbackUrl":"https://api.example.com/workflows/665f1b2c3d4e5f6071829304","workflow":{"id":"d64b3aff57a1b5528b6dc5d2","name":"internship-workflow"},"stage":{"operation":"forwarder"},"tenant":{"organisationId":"org-1"},"storage":{"secret":"SECRET"},"signedUrl":"https://example.com?secret=SECRET"}`

func TestHandleInvocation(t *testing.T) {
	result, callbackURL, err := handleInvocation([]byte(sampleInvocation), "forwarder", 1700000000, 30)
	if err != nil {
		t.Fatal(err)
	}
	if callbackURL != "https://api.example.com/workflows/665f1b2c3d4e5f6071829304" {
		t.Fatalf("unexpected callbackUrl: %s", callbackURL)
	}
	if len(result) != 3 {
		t.Fatalf("invocation result contains unexpected fields: %v", result)
	}
	var runID, operation string
	if json.Unmarshal(result["runId"], &runID) != nil || runID != "665f1b2c3d4e5f6071829304" {
		t.Fatalf("unexpected runId: %s", result["runId"])
	}
	if json.Unmarshal(result["operation"], &operation) != nil || operation != "forwarder" {
		t.Fatalf("unexpected operation: %s", result["operation"])
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "SECRET") {
		t.Fatalf("credentials leaked: %s", encoded)
	}
	var envelope struct {
		Blocks []struct {
			Type string        `json:"type"`
			Data models.Marker `json:"data"`
		} `json:"blocks"`
	}
	if err := json.Unmarshal(result["payload"], &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Blocks) != 1 || envelope.Blocks[0].Type != "marker" || envelope.Blocks[0].Data.StartTimestamp != 1700000000 || envelope.Blocks[0].Data.EndTimestamp != 1700000030 {
		t.Fatalf("unexpected marker: %+v", envelope)
	}
	replayed, _, _ := handleInvocation([]byte(sampleInvocation), "forwarder", 1700000000, 30)
	if !reflect.DeepEqual(result, replayed) {
		t.Fatal("redelivery changed the result")
	}
}

func TestHandleInvocationRejectsInvalidInput(t *testing.T) {
	for _, body := range []string{
		"broken",
		"null",
		"[]",
		"{}",
		strings.Replace(sampleInvocation, `"runId":"665f1b2c3d4e5f6071829304"`, `"runId":"../secret"`, 1),
		strings.Replace(sampleInvocation, `"operation":"forwarder"`, `"operation":""`, 1),
		strings.Replace(sampleInvocation, invocationSchemaV1, "uug.ai/workflow-invocation/v2", 1),
		strings.Replace(sampleInvocation, `"schema":"uug.ai/workflow-invocation/v1",`, "", 1),
		strings.Replace(sampleInvocation, `"callbackUrl":"https://api.example.com/workflows/665f1b2c3d4e5f6071829304",`, "", 1),
		strings.Replace(sampleInvocation, `"callbackUrl":"https://api.example.com/workflows/665f1b2c3d4e5f6071829304"`, `"callbackUrl":"https://api.example.com/workflows/another-run"`, 1),
	} {
		if _, _, err := handleInvocation([]byte(body), "forwarder", 1700000000, 30); err == nil {
			t.Errorf("accepted invalid invocation: %s", body)
		}
	}
	for _, interval := range [][2]int64{{0, 30}, {1700000000000, 30}, {1700000000, 0}, {1700000000, 3601}} {
		if _, _, err := handleInvocation([]byte(sampleInvocation), "forwarder", interval[0], interval[1]); err == nil {
			t.Errorf("accepted invalid interval: %v", interval)
		}
	}
	mismatch := strings.Replace(sampleInvocation, `"operation":"forwarder"`, `"operation":"loitering"`, 1)
	if _, _, err := handleInvocation([]byte(mismatch), "forwarder", 1700000000, 30); err == nil || !strings.Contains(err.Error(), `"loitering"`) {
		t.Fatalf("operation mismatch was not explained: %v", err)
	}
}
