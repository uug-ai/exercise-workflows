package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	ingest "github.com/uug-ai/ingest/pkg/ingest"
	"github.com/uug-ai/models/pkg/models"
)

const operation = "loitering"

var runIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

func measureLoitering(stageOperation string, start, duration int64) models.Marker {
	return models.Marker{
		Name:           stageOperation + "-example",
		Description:    "Synthetic tutorial marker; not a real loitering detection",
		StartTimestamp: start,
		EndTimestamp:   start + duration,
	}
}

func handleRun(body []byte, stageOperation string, start, duration int64) (map[string]json.RawMessage, error) {
	var run map[string]json.RawMessage
	if json.Unmarshal(body, &run) != nil || run == nil {
		return nil, errors.New("expected a WorkflowRun JSON object")
	}
	for _, field := range []string{"runId", "operation", "key"} {
		var value string
		if json.Unmarshal(run[field], &value) != nil || value == "" {
			return nil, fmt.Errorf("missing or invalid %s", field)
		}
		if field == "operation" && value != stageOperation {
			return nil, errors.New("dispatch operation does not match STAGE_OPERATION")
		}
		if field == "runId" && !runIDPattern.MatchString(value) {
			return nil, errors.New("invalid runId")
		}
	}
	if start < 1 || start > 253402300799 || duration < 1 || duration > 3600 || start > 253402300799-duration {
		return nil, errors.New("provide a valid Unix-seconds marker start and duration of 1..3600 seconds")
	}

	marker := measureLoitering(stageOperation, start, duration)
	data, err := json.Marshal(marker)
	if err != nil {
		return nil, err
	}
	envelope, err := json.Marshal(ingest.BlockEnvelope{
		Blocks: []ingest.Block{{Type: ingest.KindMarker, Data: data}},
	})
	if err != nil {
		return nil, err
	}

	result := run
	if raw, exists := result["user"]; exists && string(raw) != "null" {
		var user map[string]json.RawMessage
		if json.Unmarshal(raw, &user) != nil || user == nil {
			return nil, errors.New("invalid user context")
		}
		delete(user, "storage")
		result["user"], _ = json.Marshal(user)
	}
	delete(result, "storage")
	delete(result, "signedUrl")
	delete(result, "results")
	result["payload"] = envelope
	return result, nil
}
