package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	ingest "github.com/uug-ai/ingest/pkg/ingest"
	"github.com/uug-ai/models/pkg/models"
)

const operation = "forwarder"
const invocationSchemaV1 = models.WorkflowInvocationSchemaV1

var runIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

type handledInvocation struct {
	runID    string
	callback *models.WorkflowCallback
	result   models.WorkflowResult
}

func measureLoitering(stageOperation string, start, duration int64) models.Marker {
	return models.Marker{
		Name:           stageOperation + "-example",
		Description:    "Synthetic tutorial marker; not a real loitering detection",
		StartTimestamp: start,
		EndTimestamp:   start + duration,
	}
}

func handleInvocation(body []byte, stageOperation string, start, duration int64) (handledInvocation, error) {
	var invocation models.WorkflowInvocation
	if json.Unmarshal(body, &invocation) != nil {
		return handledInvocation{}, errors.New("expected a workflow invocation JSON object")
	}
	runID, dispatchOperation, callback, err := dispatchMetadata(invocation)
	if err != nil {
		return handledInvocation{}, err
	}
	if dispatchOperation != stageOperation {
		return handledInvocation{}, fmt.Errorf("dispatch operation %q does not match STAGE_OPERATION %q", dispatchOperation, stageOperation)
	}
	if !runIDPattern.MatchString(runID) {
		return handledInvocation{}, errors.New("invalid runId")
	}
	if callback != nil && callback.URL != "" {
		if err := validateCallback(callback, runID); err != nil {
			return handledInvocation{}, err
		}
	}
	if start < 1 || start > 253402300799 || duration < 1 || duration > 3600 || start > 253402300799-duration {
		return handledInvocation{}, errors.New("provide a valid Unix-seconds marker start and duration of 1..3600 seconds")
	}

	marker := measureLoitering(stageOperation, start, duration)
	data, err := json.Marshal(marker)
	if err != nil {
		return handledInvocation{}, err
	}

	return handledInvocation{
		runID:    runID,
		callback: callback,
		result: models.WorkflowResult{
			Schema: models.WorkflowResultSchemaV1,
			Stage:  models.WorkflowStageReference{Operation: dispatchOperation},
			Result: map[string]any{
				"blocks": []ingest.Block{{Type: ingest.KindMarker, Data: data}},
			},
		},
	}, nil
}

func dispatchMetadata(invocation models.WorkflowInvocation) (runID, dispatchOperation string, callback *models.WorkflowCallback, err error) {
	if invocation.RunID == "" {
		return "", "", nil, errors.New("missing or invalid runId")
	}

	if invocation.Schema == "" {
		return "", "", nil, errors.New("missing or invalid schema")
	}
	if invocation.Schema != models.WorkflowInvocationSchemaV1 {
		return "", "", nil, fmt.Errorf("unsupported invocation schema %q", invocation.Schema)
	}
	if invocation.Stage.Operation == "" {
		return "", "", nil, errors.New("missing or invalid stage.operation")
	}
	return invocation.RunID, invocation.Stage.Operation, invocation.Callback, nil
}
