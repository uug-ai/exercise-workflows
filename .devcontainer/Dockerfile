FROM mcr.microsoft.com/devcontainers/go:1.25-bookworm

ARG TARGETARCH
ARG YOLO_MODEL_URL=https://github.com/ultralytics/assets/releases/download/v8.3.0/yolo11n.onnx
ARG YOLO_MODEL_SHA256=634279b40c07c6391472c51ad45b81ebc48706a9a1fe72dd3396322acd0c053b

USER root

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl libopencv-dev \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /opt/yolo \
    && curl -fL "${YOLO_MODEL_URL}" -o /opt/yolo/yolo11n.onnx \
    && echo "${YOLO_MODEL_SHA256}  /opt/yolo/yolo11n.onnx" | sha256sum -c -

RUN if [ "${TARGETARCH}" = "amd64" ]; then \
        curl -fL https://github.com/microsoft/onnxruntime/releases/download/v1.29.0/onnxruntime-linux-x64-1.29.0.tgz -o /tmp/onnxruntime.tgz \
        && echo "c3fddc4f139a045b0c4902c57410f0694f1c2fdf9b6939fbe38b1aeae7cd14ba  /tmp/onnxruntime.tgz" | sha256sum -c -; \
    elif [ "${TARGETARCH}" = "arm64" ]; then \
        curl -fL https://github.com/microsoft/onnxruntime/releases/download/v1.29.0/onnxruntime-linux-aarch64-1.29.0.tgz -o /tmp/onnxruntime.tgz \
        && echo "e1799098ebc054b370f6176a450f158720f297818c613e5dc99b92e2ec82346f  /tmp/onnxruntime.tgz" | sha256sum -c -; \
    else \
        echo "Unsupported architecture: ${TARGETARCH}" >&2; exit 1; \
    fi \
    && mkdir -p /opt/onnxruntime \
    && tar -xzf /tmp/onnxruntime.tgz -C /opt/onnxruntime --strip-components=1 \
    && rm /tmp/onnxruntime.tgz

ENV YOLO_MODEL=/opt/yolo/yolo11n.onnx
ENV ONNXRUNTIME_SHARED_LIBRARY=/opt/onnxruntime/lib/libonnxruntime.so.1.29.0

USER vscode
