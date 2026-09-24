package app

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/uug-ai/models/pkg/models"
)

const sampleInvocation = `{"schema":"uug.ai/workflow-invocation/v1","executionId":"wfx_example","runId":"665f1b2c3d4e5f6071829304","callback":{"url":"https://api.example.com/workflows/runs/665f1b2c3d4e5f6071829304","method":"POST","resultSchema":"uug.ai/workflow-result/v1"},"workflow":{"id":"d64b3aff57a1b5528b6dc5d2","name":"internship-workflow"},"stage":{"operation":"person-detection"},"tenant":{"organisationId":"org-1"},"media":{"key":"recording.mp4","signedUrl":"https://example.com?secret=SECRET"},"data":{"storage":{"secret":"SECRET"}}}`

func TestHandleInvocation(t *testing.T) {
	invocation, err := handleInvocation([]byte(sampleInvocation), "person-detection", 1700000000, 30)
	if err != nil {
		t.Fatal(err)
	}
	if invocation.callback.URL != "https://api.example.com/workflows/runs/665f1b2c3d4e5f6071829304" {
		t.Fatalf("unexpected callback URL: %s", invocation.callback.URL)
	}
	if invocation.runID != "665f1b2c3d4e5f6071829304" {
		t.Fatalf("unexpected runId: %s", invocation.runID)
	}
	if invocation.result.Schema != models.WorkflowResultSchemaV1 || invocation.result.Stage.Operation != "person-detection" {
		t.Fatalf("unexpected result contract: %+v", invocation.result)
	}
	encoded, _ := json.Marshal(invocation.result)
	if strings.Contains(string(encoded), "SECRET") {
		t.Fatalf("credentials leaked: %s", encoded)
	}
	var envelope struct {
		Blocks []struct {
			Type string        `json:"type"`
			Data models.Marker `json:"data"`
		} `json:"blocks"`
	}
	if invocation.result.Result != nil {
		t.Fatalf("marker callback unexpectedly contains routing result: %+v", invocation.result.Result)
	}
	if err := json.Unmarshal(invocation.result.Payload, &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Blocks) != 1 || envelope.Blocks[0].Type != "marker" || envelope.Blocks[0].Data.Name != customMarkerName || envelope.Blocks[0].Data.Description != "Created by the custom workflow integration" || envelope.Blocks[0].Data.StartTimestamp != 1700000000 || envelope.Blocks[0].Data.EndTimestamp != 1700000030 {
		t.Fatalf("unexpected marker: %+v", envelope)
	}
	replayed, _ := handleInvocation([]byte(sampleInvocation), "person-detection", 1700000000, 30)
	if !reflect.DeepEqual(invocation, replayed) {
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
		strings.Replace(sampleInvocation, `"operation":"person-detection"`, `"operation":""`, 1),
		strings.Replace(sampleInvocation, invocationSchemaV1, "uug.ai/workflow-invocation/v2", 1),
		strings.Replace(sampleInvocation, `"schema":"uug.ai/workflow-invocation/v1",`, "", 1),
		strings.Replace(sampleInvocation, `"url":"https://api.example.com/workflows/runs/665f1b2c3d4e5f6071829304"`, `"url":"https://api.example.com/workflows/runs/another-run"`, 1),
		strings.Replace(sampleInvocation, `"method":"POST"`, `"method":"GET"`, 1),
		strings.Replace(sampleInvocation, `"resultSchema":"uug.ai/workflow-result/v1"`, `"resultSchema":"uug.ai/workflow-result/v2"`, 1),
	} {
		if _, err := handleInvocation([]byte(body), "person-detection", 1700000000, 30); err == nil {
			t.Errorf("accepted invalid invocation: %s", body)
		}
	}

	for _, interval := range [][2]int64{{0, 30}, {1700000000000, 30}, {1700000000, 0}, {1700000000, 3601}} {
		if _, err := handleInvocation([]byte(sampleInvocation), "person-detection", interval[0], interval[1]); err == nil {
			t.Errorf("accepted invalid interval: %v", interval)
		}
	}
	mismatch := strings.Replace(sampleInvocation, `"operation":"person-detection"`, `"operation":"other-operation"`, 1)
	if _, err := handleInvocation([]byte(mismatch), "person-detection", 1700000000, 30); err == nil || !strings.Contains(err.Error(), `"other-operation"`) {
		t.Fatalf("operation mismatch was not explained: %v", err)
	}
}

func TestHandleInvocationAllowsMissingCallback(t *testing.T) {
	body := strings.Replace(sampleInvocation, `"callback":{"url":"https://api.example.com/workflows/runs/665f1b2c3d4e5f6071829304","method":"POST","resultSchema":"uug.ai/workflow-result/v1"},`, "", 1)
	invocation, err := handleInvocation([]byte(body), "person-detection", 1700000000, 30)
	if err != nil {
		t.Fatal(err)
	}
	if invocation.callback != nil {
		t.Fatalf("unexpected callback: %+v", invocation.callback)
	}
}
