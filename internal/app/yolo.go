package app

import (
	"context"
	"errors"
	"fmt"
	"image"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
	"gocv.io/x/gocv"
)

const yoloInputSize = 640

var onnxRuntimeMu sync.Mutex

type personDetection struct {
	Detected       bool
	FramesAnalyzed int
	MaxConfidence  float32
}

type personDetector interface {
	Detect(context.Context, []byte) (personDetection, error)
}

type yoloPersonDetector struct {
	modelPath, runtimePath string
	confidence             float32
	sampleFPS              float64
}

func newYOLOPersonDetector() (*yoloPersonDetector, error) {
	confidence, err := strconv.ParseFloat(envOr("YOLO_CONFIDENCE", "0.5"), 32)
	if err != nil || confidence <= 0 || confidence > 1 {
		return nil, errors.New("YOLO_CONFIDENCE must be greater than 0 and at most 1")
	}
	sampleFPS, err := strconv.ParseFloat(envOr("YOLO_SAMPLE_FPS", "1"), 64)
	if err != nil || sampleFPS <= 0 || sampleFPS > 30 {
		return nil, errors.New("YOLO_SAMPLE_FPS must be greater than 0 and at most 30")
	}
	modelPath, err := regularFileFromEnv("YOLO_MODEL", "/opt/yolo/yolo11n.onnx")
	if err != nil {
		return nil, err
	}
	runtimePath, err := regularFileFromEnv("ONNXRUNTIME_SHARED_LIBRARY", "/opt/onnxruntime/lib/libonnxruntime.so.1.29.0")
	if err != nil {
		return nil, err
	}
	return &yoloPersonDetector{
		modelPath:   modelPath,
		runtimePath: runtimePath,
		confidence:  float32(confidence),
		sampleFPS:   sampleFPS,
	}, nil
}

func regularFileFromEnv(name, fallback string) (string, error) {
	path := strings.TrimSpace(envOr(name, fallback))
	if path == "" || strings.ContainsAny(path, "\r\n") {
		return "", fmt.Errorf("%s must identify a regular file", name)
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s must identify an existing regular file", name)
	}
	return path, nil
}

func (detector *yoloPersonDetector) Detect(ctx context.Context, media []byte) (personDetection, error) {
	if len(media) == 0 {
		return personDetection{}, errors.New("cannot run YOLO on empty media")
	}
	mediaFile, err := os.CreateTemp("", "workflow-media-*.mp4")
	if err != nil {
		return personDetection{}, errors.New("cannot create private temporary media file")
	}
	mediaPath := mediaFile.Name()
	defer os.Remove(mediaPath)
	if _, err := mediaFile.Write(media); err != nil {
		mediaFile.Close()
		return personDetection{}, errors.New("cannot stage media for YOLO")
	}
	if err := mediaFile.Close(); err != nil {
		return personDetection{}, errors.New("cannot finish staging media for YOLO")
	}

	capture, err := gocv.VideoCaptureFile(mediaPath)
	if err != nil {
		return personDetection{}, errors.New("YOLO could not open the downloaded media as video")
	}
	defer capture.Close()
	fps := capture.Get(gocv.VideoCaptureFPS)
	if fps <= 0 || math.IsNaN(fps) || math.IsInf(fps, 0) {
		return personDetection{}, errors.New("YOLO could not determine the video frame rate")
	}

	onnxRuntimeMu.Lock()
	defer onnxRuntimeMu.Unlock()
	if !ort.IsInitialized() {
		ort.SetSharedLibraryPath(detector.runtimePath)
		if err := ort.InitializeEnvironment(); err != nil {
			return personDetection{}, errors.New("YOLO could not initialize ONNX Runtime")
		}
	}
	session, err := ort.NewDynamicAdvancedSession(
		detector.modelPath,
		[]string{"images"},
		[]string{"output0"},
		nil,
	)
	if err != nil {
		return personDetection{}, errors.New("YOLO could not load YOLO_MODEL")
	}
	defer session.Destroy()

	sampleEvery := max(1, int(math.Round(fps/detector.sampleFPS)))
	frame := gocv.NewMat()
	defer frame.Close()
	detection := personDetection{}
	for frameIndex := 0; ; frameIndex++ {
		if err := ctx.Err(); err != nil {
			return personDetection{}, fmt.Errorf("YOLO person detection canceled: %w", err)
		}
		if ok := capture.Read(&frame); !ok {
			break
		}
		if frame.Empty() || frameIndex%sampleEvery != 0 {
			continue
		}
		confidence, err := detectPersonInFrame(session, frame)
		if err != nil {
			return personDetection{}, err
		}
		detection.FramesAnalyzed++
		if confidence >= detector.confidence {
			detection.Detected = true
			detection.MaxConfidence = confidence
			return detection, nil
		}
	}
	if detection.FramesAnalyzed == 0 {
		return personDetection{}, errors.New("YOLO did not decode any video frames")
	}
	return detection, nil
}

func detectPersonInFrame(session *ort.DynamicAdvancedSession, frame gocv.Mat) (float32, error) {
	inputData, err := yoloInput(frame)
	if err != nil {
		return 0, err
	}
	input, err := ort.NewTensor(ort.NewShape(1, 3, yoloInputSize, yoloInputSize), inputData)
	if err != nil {
		return 0, errors.New("YOLO could not create an input tensor")
	}
	defer input.Destroy()
	outputs := []ort.Value{nil}
	if err := session.Run([]ort.Value{input}, outputs); err != nil {
		return 0, errors.New("YOLO inference failed")
	}
	defer outputs[0].Destroy()
	output, ok := outputs[0].(*ort.Tensor[float32])
	if !ok {
		return 0, errors.New("YOLO returned an unsupported output tensor")
	}
	return personConfidence(output.GetData(), output.GetShape())
}

func yoloInput(frame gocv.Mat) ([]float32, error) {
	resized := gocv.NewMat()
	defer resized.Close()
	gocv.Resize(frame, &resized, image.Pt(yoloInputSize, yoloInputSize), 0, 0, gocv.InterpolationLinear)
	if resized.Empty() || resized.Channels() != 3 {
		return nil, errors.New("YOLO could not prepare a three-channel video frame")
	}
	pixels := resized.ToBytes()
	pixelCount := yoloInputSize * yoloInputSize
	if len(pixels) != pixelCount*3 {
		return nil, errors.New("YOLO received an unexpected video frame layout")
	}
	input := make([]float32, pixelCount*3)
	for pixel := 0; pixel < pixelCount; pixel++ {
		source := pixel * 3
		input[pixel] = float32(pixels[source+2]) / 255
		input[pixelCount+pixel] = float32(pixels[source+1]) / 255
		input[2*pixelCount+pixel] = float32(pixels[source]) / 255
	}
	return input, nil
}

func personConfidence(output []float32, shape ort.Shape) (float32, error) {
	if len(shape) != 3 || shape[0] != 1 {
		return 0, fmt.Errorf("YOLO returned unsupported output shape %v", shape)
	}
	rows, columns := int(shape[1]), int(shape[2])
	if len(output) != rows*columns {
		return 0, errors.New("YOLO returned inconsistent output data")
	}
	var confidence float32
	switch {
	case rows == 84:
		for detection := 0; detection < columns; detection++ {
			confidence = max(confidence, output[4*columns+detection])
		}
	case columns == 84:
		for detection := 0; detection < rows; detection++ {
			confidence = max(confidence, output[detection*columns+4])
		}
	default:
		return 0, fmt.Errorf("YOLO returned unsupported output shape %v", shape)
	}
	if confidence < 0 || confidence > 1 || math.IsNaN(float64(confidence)) || math.IsInf(float64(confidence), 0) {
		return 0, errors.New("YOLO returned an invalid person confidence")
	}
	return confidence, nil
}
