package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"time"

	log "github.com/sirupsen/logrus"
	ingest "github.com/uug-ai/ingest/pkg/ingest"
	"github.com/uug-ai/models/pkg/models"
)

const operation = "forwarder"
const invocationSchemaV1 = models.WorkflowInvocationSchemaV1
const personMarkerName = "person detected"
const maxMediaSize = 100 << 20

var runIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)
var signedMediaClient = &http.Client{Timeout: 2 * time.Minute}

type handledInvocation struct {
	runID    string
	callback *models.WorkflowCallback
	result   models.WorkflowResult
}

func buildPersonMarker(invocation models.WorkflowInvocation, detection personDetection, start, duration int64) models.Marker {
	marker := models.Marker{
		OrganisationId: invocation.Tenant.OrganisationID,
		MediaKeys:      []string{invocation.Media.Key},
		Name:           personMarkerName,
		Description:    fmt.Sprintf("YOLO detected a person with %.2f confidence after analyzing %d sampled frames", detection.MaxConfidence, detection.FramesAnalyzed),
		StartTimestamp: start,
		EndTimestamp:   start + duration,
		Duration:       duration,
	}
	if len(invocation.Device.SiteIDs) > 0 {
		marker.SiteId = invocation.Device.SiteIDs[0]
	}
	return marker
}

func handleInvocation(ctx context.Context, body []byte, stageOperation string, start, duration int64, detector personDetector) (handledInvocation, error) {
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

	logWorkflowMedia(invocation)

	media, err := downloadSignedMedia(ctx, invocation.Media.SignedURL)
	if err != nil {
		return handledInvocation{}, err
	}
	log.WithFields(log.Fields{
		"component":        "workflow_stage",
		"event":            "workflow_media_downloaded",
		"media_key":        invocation.Media.Key,
		"media_size_bytes": len(media),
	}).Info("Workflow media downloaded into memory")

	if detector == nil {
		return handledInvocation{}, errors.New("YOLO person detector is not configured")
	}
	detection, err := detector.Detect(ctx, media)
	if err != nil {
		return handledInvocation{}, err
	}
	log.WithFields(log.Fields{
		"component":       "workflow_stage",
		"event":           "yolo_person_detection_completed",
		"frames_analyzed": detection.FramesAnalyzed,
		"max_confidence":  detection.MaxConfidence,
		"person_detected": detection.Detected,
	}).Info("YOLO person detection completed")

	result := models.WorkflowResult{
		Schema: models.WorkflowResultSchemaV1,
		Stage:  models.WorkflowStageReference{Operation: dispatchOperation},
	}
	if detection.Detected {
		marker := buildPersonMarker(invocation, detection, start, duration)
		markerData, err := json.Marshal(marker)
		if err != nil {
			return handledInvocation{}, err
		}
		mediaPatchData, err := json.Marshal(struct {
			MediaKey    string `json:"mediaKey"`
			Description string `json:"description"`
		}{
			MediaKey:    invocation.Media.Key,
			Description: marker.Description,
		})
		if err != nil {
			return handledInvocation{}, err
		}
		payload, err := json.Marshal(struct {
			Blocks []ingest.Block `json:"blocks"`
		}{
			Blocks: []ingest.Block{
				{Type: ingest.KindMarker, Data: markerData},
				{Type: ingest.KindMediaPatch, Data: mediaPatchData},
			},
		})
		if err != nil {
			return handledInvocation{}, err
		}
		result.Payload = payload
	} else {
		result.Result = map[string]any{
			"personDetected": false,
			"framesAnalyzed": detection.FramesAnalyzed,
		}
	}

	return handledInvocation{
		runID:    runID,
		callback: callback,
		result:   result,
	}, nil
}

func downloadSignedMedia(ctx context.Context, signedURL string) ([]byte, error) {
	endpoint, err := url.Parse(signedURL)
	if err != nil || endpoint.Host == "" || endpoint.User != nil || endpoint.Scheme != "http" && endpoint.Scheme != "https" {
		return nil, errors.New("media.signedUrl must be an absolute HTTP or HTTPS URL without user credentials")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, errors.New("cannot create signed media request")
	}
	response, err := signedMediaClient.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("signed media download canceled: %w", ctx.Err())
		}
		return nil, errors.New("signed media download request failed")
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("signed media download returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > maxMediaSize {
		return nil, fmt.Errorf("signed media exceeds the %d-byte in-memory limit", maxMediaSize)
	}
	media, err := io.ReadAll(io.LimitReader(response.Body, maxMediaSize+1))
	if err != nil {
		return nil, errors.New("cannot read signed media response")
	}
	if len(media) > maxMediaSize {
		return nil, fmt.Errorf("signed media exceeds the %d-byte in-memory limit", maxMediaSize)
	}
	return media, nil
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
	if invocation.Media.Key == "" {
		return "", "", nil, errors.New("missing or invalid media.key")
	}
	return invocation.RunID, invocation.Stage.Operation, invocation.Callback, nil
}
