package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"

	ingest "github.com/uug-ai/ingest/pkg/ingest"
	"github.com/uug-ai/models/pkg/models"
)

const operation = "forwarder"
const invocationSchemaV1 = "uug.ai/workflow-invocation/v1"

var runIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

func measureLoitering(stageOperation string, start, duration int64) models.Marker {
	return models.Marker{
		Name:           stageOperation + "-example",
		Description:    "Synthetic tutorial marker; not a real loitering detection",
		StartTimestamp: start,
		EndTimestamp:   start + duration,
	}
}

func handleInvocation(body []byte, stageOperation string, start, duration int64) (map[string]json.RawMessage, string, error) {
	var invocation map[string]json.RawMessage
	if json.Unmarshal(body, &invocation) != nil || invocation == nil {
		return nil, "", errors.New("expected a workflow invocation JSON object")
	}
	runID, dispatchOperation, callbackURL, err := dispatchMetadata(invocation)
	if err != nil {
		return nil, "", err
	}
	if dispatchOperation != stageOperation {
		return nil, "", fmt.Errorf("dispatch operation %q does not match STAGE_OPERATION %q", dispatchOperation, stageOperation)
	}
	if !runIDPattern.MatchString(runID) {
		return nil, "", errors.New("invalid runId")
	}
	if err := validateCallback(callbackURL, runID); err != nil {
		return nil, "", err
	}
	if start < 1 || start > 253402300799 || duration < 1 || duration > 3600 || start > 253402300799-duration {
		return nil, "", errors.New("provide a valid Unix-seconds marker start and duration of 1..3600 seconds")
	}

	marker := measureLoitering(stageOperation, start, duration)
	data, err := json.Marshal(marker)
	if err != nil {
		return nil, "", err
	}
	envelope, err := json.Marshal(ingest.BlockEnvelope{
		Blocks: []ingest.Block{{Type: ingest.KindMarker, Data: data}},
	})
	if err != nil {
		return nil, "", err
	}

	result := map[string]json.RawMessage{
		"runId":     invocation["runId"],
		"operation": json.RawMessage(strconv.Quote(dispatchOperation)),
	}
	result["payload"] = envelope
	return result, callbackURL, nil
}

func dispatchMetadata(invocation map[string]json.RawMessage) (runID, dispatchOperation, callbackURL string, err error) {
	if json.Unmarshal(invocation["runId"], &runID) != nil || runID == "" {
		return "", "", "", errors.New("missing or invalid runId")
	}

	var schema string
	if json.Unmarshal(invocation["schema"], &schema) != nil || schema == "" {
		return "", "", "", errors.New("missing or invalid schema")
	}
	if schema != invocationSchemaV1 {
		return "", "", "", fmt.Errorf("unsupported invocation schema %q", schema)
	}
	var stage struct {
		Operation string `json:"operation"`
	}
	if json.Unmarshal(invocation["stage"], &stage) != nil || stage.Operation == "" {
		return "", "", "", errors.New("missing or invalid stage.operation")
	}
	if json.Unmarshal(invocation["callbackUrl"], &callbackURL) != nil || callbackURL == "" {
		return "", "", "", errors.New("missing or invalid callbackUrl; this delivery may predate callback URL support, so dispatch a fresh run or ask the facilitator to quarantine it")
	}
	return runID, stage.Operation, callbackURL, nil
}
