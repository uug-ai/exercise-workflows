package app

import (
	"context"
	"os"
	"strings"
	"testing"

	ort "github.com/yalue/onnxruntime_go"
	"gocv.io/x/gocv"
)

func TestPersonConfidence(t *testing.T) {
	channelsFirst := make([]float32, 84*3)
	channelsFirst[4*3+1] = 0.87
	confidence, err := personConfidence(channelsFirst, ort.NewShape(1, 84, 3))
	if err != nil || confidence != 0.87 {
		t.Fatalf("channels-first confidence = %v, error = %v", confidence, err)
	}

	detectionsFirst := make([]float32, 3*84)
	detectionsFirst[2*84+4] = 0.76
	confidence, err = personConfidence(detectionsFirst, ort.NewShape(1, 3, 84))
	if err != nil || confidence != 0.76 {
		t.Fatalf("detections-first confidence = %v, error = %v", confidence, err)
	}

	if _, err := personConfidence(make([]float32, 4), ort.NewShape(2, 2)); err == nil {
		t.Fatal("accepted unsupported YOLO output")
	}
}

func TestYOLOConfiguration(t *testing.T) {
	directory := t.TempDir()
	modelPath := directory + "/yolo.onnx"
	runtimePath := directory + "/onnxruntime.so"
	for _, path := range []string{modelPath, runtimePath} {
		if err := os.WriteFile(path, []byte("runtime"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("YOLO_MODEL", modelPath)
	t.Setenv("ONNXRUNTIME_SHARED_LIBRARY", runtimePath)
	detector, err := newYOLOPersonDetector()
	if err != nil {
		t.Fatal(err)
	}
	if detector.modelPath != modelPath || detector.runtimePath != runtimePath || detector.confidence != 0.5 || detector.sampleFPS != 1 {
		t.Fatalf("unexpected detector configuration: %+v", detector)
	}

	for name, value := range map[string]string{
		"YOLO_CONFIDENCE":            "1.1",
		"YOLO_SAMPLE_FPS":            "0",
		"YOLO_MODEL":                 modelPath + "-missing",
		"ONNXRUNTIME_SHARED_LIBRARY": runtimePath + "-missing",
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("YOLO_MODEL", modelPath)
			t.Setenv("ONNXRUNTIME_SHARED_LIBRARY", runtimePath)
			t.Setenv(name, value)
			if _, err := newYOLOPersonDetector(); err == nil || strings.TrimSpace(err.Error()) == "" {
				t.Fatalf("accepted %s=%q", name, value)
			}
		})
	}
}

func TestYOLOModelIntegration(t *testing.T) {
	modelPath := os.Getenv("YOLO_MODEL_INTEGRATION")
	runtimePath := os.Getenv("ONNXRUNTIME_SHARED_LIBRARY_INTEGRATION")
	if modelPath == "" || runtimePath == "" {
		t.Skip("set YOLO_MODEL_INTEGRATION and ONNXRUNTIME_SHARED_LIBRARY_INTEGRATION to exercise an ONNX model")
	}
	videoPath := t.TempDir() + "/blank.avi"
	writer, err := gocv.VideoWriterFile(videoPath, "MJPG", 10, yoloInputSize, yoloInputSize, true)
	if err != nil {
		t.Fatal(err)
	}
	frame := gocv.NewMatWithSize(yoloInputSize, yoloInputSize, gocv.MatTypeCV8UC3)
	for range 30 {
		if err := writer.Write(frame); err != nil {
			t.Fatal(err)
		}
	}
	frame.Close()
	writer.Close()
	media, err := os.ReadFile(videoPath)
	if err != nil {
		t.Fatal(err)
	}
	detector := yoloPersonDetector{
		modelPath:   modelPath,
		runtimePath: runtimePath,
		confidence:  0.5,
		sampleFPS:   1,
	}
	detection, err := detector.Detect(context.Background(), media)
	if err != nil {
		t.Fatal(err)
	}
	defer ort.DestroyEnvironment()
	if detection.Detected || detection.FramesAnalyzed != 3 {
		t.Fatalf("unexpected blank-video detection: %+v", detection)
	}
}
