package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	ingest "github.com/uug-ai/ingest/pkg/ingest"
	"github.com/uug-ai/models/pkg/models"
)

const sampleInvocation = `{"schema":"uug.ai/workflow-invocation/v1","executionId":"wfx_example","runId":"665f1b2c3d4e5f6071829304","callback":{"url":"https://api.example.com/workflows/runs/665f1b2c3d4e5f6071829304","method":"POST","resultSchema":"uug.ai/workflow-result/v1"},"workflow":{"id":"d64b3aff57a1b5528b6dc5d2","name":"internship-workflow"},"stage":{"operation":"forwarder"},"tenant":{"organisationId":"org-1"},"media":{"key":"recording.mp4","signedUrl":"https://example.com?secret=SECRET"},"device":{"key":"agent1","siteIds":["site-1"]},"data":{"storage":{"secret":"SECRET"}}}`

type stubPersonDetector struct {
	detection personDetection
	err       error
	media     []byte
}

func (detector *stubPersonDetector) Detect(_ context.Context, media []byte) (personDetection, error) {
	detector.media = append([]byte(nil), media...)
	return detector.detection, detector.err
}

func detectedPerson() *stubPersonDetector {
	return &stubPersonDetector{
		detection: personDetection{Detected: true, FramesAnalyzed: 3, MaxConfidence: 0.91},
	}
}

func newMediaServer(t *testing.T, media []byte) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			t.Errorf("media request method = %s, want GET", request.Method)
		}
		_, _ = writer.Write(media)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestHandleInvocation(t *testing.T) {
	server := newMediaServer(t, []byte("video"))
	body := strings.Replace(sampleInvocation, "https://example.com?secret=SECRET", server.URL, 1)
	detector := detectedPerson()
	invocation, err := handleInvocation(context.Background(), []byte(body), "forwarder", 1700000000, 30, detector)
	if err != nil {
		t.Fatal(err)
	}
	if string(detector.media) != "video" {
		t.Fatalf("detector received %q", detector.media)
	}
	if invocation.callback.URL != "https://api.example.com/workflows/runs/665f1b2c3d4e5f6071829304" {
		t.Fatalf("unexpected callback URL: %s", invocation.callback.URL)
	}
	if invocation.runID != "665f1b2c3d4e5f6071829304" {
		t.Fatalf("unexpected runId: %s", invocation.runID)
	}
	if invocation.result.Schema != models.WorkflowResultSchemaV1 || invocation.result.Stage.Operation != "forwarder" {
		t.Fatalf("unexpected result contract: %+v", invocation.result)
	}
	if invocation.result.Result != nil {
		t.Fatalf("marker blocks must not be returned as routing results: %+v", invocation.result.Result)
	}
	encoded, _ := json.Marshal(invocation.result)
	if strings.Contains(string(encoded), "SECRET") {
		t.Fatalf("credentials leaked: %s", encoded)
	}
	var envelope struct {
		Blocks []ingest.Block `json:"blocks"`
	}
	if err := json.Unmarshal(invocation.result.Payload, &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Blocks) != 2 {
		t.Fatalf("unexpected marker blocks: %+v", envelope)
	}
	var marker models.Marker
	if err := json.Unmarshal(envelope.Blocks[0].Data, &marker); err != nil {
		t.Fatal(err)
	}
	if envelope.Blocks[0].Type != "marker" ||
		marker.Name != personMarkerName ||
		!strings.Contains(marker.Description, "0.91 confidence") ||
		marker.StartTimestamp != 1700000000 ||
		marker.EndTimestamp != 1700000030 ||
		marker.Duration != 30 ||
		marker.OrganisationId != "org-1" ||
		marker.SiteId != "site-1" ||
		!reflect.DeepEqual(marker.MediaKeys, []string{"recording.mp4"}) {
		t.Fatalf("unexpected marker: %+v", envelope)
	}
	var mediaPatch struct {
		MediaKey    string `json:"mediaKey"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(envelope.Blocks[1].Data, &mediaPatch); err != nil {
		t.Fatal(err)
	}
	if envelope.Blocks[1].Type != ingest.KindMediaPatch ||
		mediaPatch.MediaKey != "recording.mp4" ||
		mediaPatch.Description != marker.Description {
		t.Fatalf("unexpected media patch: %+v", envelope.Blocks[1])
	}
	replayed, _ := handleInvocation(context.Background(), []byte(body), "forwarder", 1700000000, 30, detectedPerson())
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
		strings.Replace(sampleInvocation, `"operation":"forwarder"`, `"operation":""`, 1),
		strings.Replace(sampleInvocation, invocationSchemaV1, "uug.ai/workflow-invocation/v2", 1),
		strings.Replace(sampleInvocation, `"schema":"uug.ai/workflow-invocation/v1",`, "", 1),
		strings.Replace(sampleInvocation, `"url":"https://api.example.com/workflows/runs/665f1b2c3d4e5f6071829304"`, `"url":"https://api.example.com/workflows/runs/another-run"`, 1),
		strings.Replace(sampleInvocation, `"method":"POST"`, `"method":"GET"`, 1),
		strings.Replace(sampleInvocation, `"resultSchema":"uug.ai/workflow-result/v1"`, `"resultSchema":"uug.ai/workflow-result/v2"`, 1),
		strings.Replace(sampleInvocation, `"signedUrl":"https://example.com?secret=SECRET"`, `"signedUrl":""`, 1),
		strings.Replace(sampleInvocation, `"key":"recording.mp4"`, `"key":""`, 1),
	} {
		if _, err := handleInvocation(context.Background(), []byte(body), "forwarder", 1700000000, 30, detectedPerson()); err == nil {
			t.Errorf("accepted invalid invocation: %s", body)
		}
	}

	for _, interval := range [][2]int64{{0, 30}, {1700000000000, 30}, {1700000000, 0}, {1700000000, 3601}} {
		if _, err := handleInvocation(context.Background(), []byte(sampleInvocation), "forwarder", interval[0], interval[1], detectedPerson()); err == nil {
			t.Errorf("accepted invalid interval: %v", interval)
		}
	}
	mismatch := strings.Replace(sampleInvocation, `"operation":"forwarder"`, `"operation":"other-operation"`, 1)
	if _, err := handleInvocation(context.Background(), []byte(mismatch), "forwarder", 1700000000, 30, detectedPerson()); err == nil || !strings.Contains(err.Error(), `"other-operation"`) {
		t.Fatalf("operation mismatch was not explained: %v", err)
	}
}

func TestDownloadSignedMedia(t *testing.T) {
	t.Run("downloads response into memory", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.Method != http.MethodGet || request.URL.Query().Get("signature") != "secret" {
				t.Errorf("unexpected signed media request: %s %s", request.Method, request.URL)
			}
			_, _ = writer.Write([]byte("video bytes"))
		}))
		t.Cleanup(server.Close)
		media, err := downloadSignedMedia(context.Background(), server.URL+"/recording.mp4?signature=secret")
		if err != nil {
			t.Fatal(err)
		}
		if string(media) != "video bytes" {
			t.Fatalf("downloaded media = %q", media)
		}
	})

	t.Run("rejects unsuccessful response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			http.Error(writer, "expired", http.StatusForbidden)
		}))
		t.Cleanup(server.Close)
		if _, err := downloadSignedMedia(context.Background(), server.URL+"?signature=secret"); err == nil || !strings.Contains(err.Error(), "HTTP 403") || strings.Contains(err.Error(), "secret") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("rejects oversized response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Length", strconv.Itoa(maxMediaSize+1))
			writer.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(server.Close)
		if _, err := downloadSignedMedia(context.Background(), server.URL); err == nil || !strings.Contains(err.Error(), "in-memory limit") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	for _, signedURL := range []string{"", "recording.mp4", "file:///tmp/recording.mp4", "https://user:password@example.com/recording.mp4"} {
		t.Run("rejects invalid URL "+signedURL, func(t *testing.T) {
			if _, err := downloadSignedMedia(context.Background(), signedURL); err == nil {
				t.Fatalf("accepted %q", signedURL)
			}
		})
	}
}

func TestHandleInvocationAllowsMissingCallback(t *testing.T) {
	body := strings.Replace(sampleInvocation, `"callback":{"url":"https://api.example.com/workflows/runs/665f1b2c3d4e5f6071829304","method":"POST","resultSchema":"uug.ai/workflow-result/v1"},`, "", 1)
	server := newMediaServer(t, []byte("video"))
	body = strings.Replace(body, "https://example.com?secret=SECRET", server.URL, 1)
	invocation, err := handleInvocation(context.Background(), []byte(body), "forwarder", 1700000000, 30, detectedPerson())
	if err != nil {
		t.Fatal(err)
	}
	if invocation.callback != nil {
		t.Fatalf("unexpected callback: %+v", invocation.callback)
	}
}

func TestHandleInvocationReturnsRoutingResultWithoutPerson(t *testing.T) {
	server := newMediaServer(t, []byte("video"))
	body := strings.Replace(sampleInvocation, "https://example.com?secret=SECRET", server.URL, 1)
	detector := &stubPersonDetector{detection: personDetection{FramesAnalyzed: 5}}
	invocation, err := handleInvocation(context.Background(), []byte(body), "forwarder", 1700000000, 30, detector)
	if err != nil {
		t.Fatal(err)
	}
	if invocation.result.Payload != nil {
		t.Fatalf("unexpected ingest payload without a person detection: %s", invocation.result.Payload)
	}
	if invocation.result.Result["personDetected"] != false || invocation.result.Result["framesAnalyzed"] != 5 {
		t.Fatalf("unexpected no-detection result: %+v", invocation.result.Result)
	}
	encoded, err := json.Marshal(invocation.result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"payload"`) || !strings.Contains(string(encoded), `"result":{"framesAnalyzed":5,"personDetected":false}`) {
		t.Fatalf("unexpected no-detection JSON: %s", encoded)
	}
}
