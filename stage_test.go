package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/uug-ai/models/pkg/models"
)

const sampleRun = `{"runId":"665f1b2c3d4e5f6071829304","operation":"loitering","key":"camera/clip.mp4","traceId":"trace-1","workflowId":"workflow-1","organisationId":"org-1","projectId":"project-1","user":{"id":"owner-1","organisationId":"org-1","projectId":"project-1","storage":{"secret_key":"SECRET"}},"device":{"deviceKey":"camera"},"storage":{"secret":"SECRET"},"signedUrl":"https://example.com?secret=SECRET","results":{"upstream":{}},"inputs":{"classify":{"objectCount":1}}}`

func TestHandleRun(t *testing.T) {
	result, err := handleRun([]byte(sampleRun), "loitering", 1700000000, 30)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "SECRET") || result["storage"] != nil || result["signedUrl"] != nil || result["results"] != nil {
		t.Fatalf("credentials or alternate result channel leaked: %s", encoded)
	}
	var original map[string]json.RawMessage
	json.Unmarshal([]byte(sampleRun), &original)
	for _, field := range []string{"runId", "key", "traceId", "workflowId", "organisationId", "projectId", "device", "inputs"} {
		if !reflect.DeepEqual(result[field], original[field]) {
			t.Errorf("changed %s", field)
		}
	}
	var user map[string]string
	json.Unmarshal(result["user"], &user)
	if user["id"] != "owner-1" || user["organisationId"] != "org-1" || user["projectId"] != "project-1" {
		t.Fatal("ownership context changed")
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
	replayed, _ := handleRun([]byte(sampleRun), "loitering", 1700000000, 30)
	if !reflect.DeepEqual(result, replayed) {
		t.Fatal("redelivery changed the result")
	}
}

func TestHandleRunRejectsInvalidInput(t *testing.T) {
	for _, body := range []string{"broken", "null", "[]", "{}", strings.Replace(sampleRun, `"runId":"665f1b2c3d4e5f6071829304"`, `"runId":"../secret"`, 1), strings.Replace(sampleRun, `"operation":"loitering"`, `"operation":"other"`, 1), strings.Replace(sampleRun, `"key":"camera/clip.mp4"`, `"key":""`, 1)} {
		if _, err := handleRun([]byte(body), "loitering", 1700000000, 30); err == nil {
			t.Errorf("accepted invalid input: %s", body)
		}
	}
	for _, interval := range [][2]int64{{0, 30}, {1700000000000, 30}, {1700000000, 0}, {1700000000, 3601}} {
		if _, err := handleRun([]byte(sampleRun), "loitering", interval[0], interval[1]); err == nil {
			t.Errorf("accepted invalid interval: %v", interval)
		}
	}
}
