FROM mcr.microsoft.com/devcontainers/go:1.25-bookworm

ARG TARGETARCH
ARG YOLO_MODEL_URL=https://github.com/ultralytics/assets/releases/download/v8.3.0/yolo11n.onnx
ARG YOLO_MODEL_SHA256=634279b40c07c6391472c51ad45b81ebc48706a9a1fe72dd3396322acd0c053b
ARG ONNXRUNTIME_VERSION=1.31.0
ARG ONNXRUNTIME_NUPKG_SHA256=7e35fdb4f5adb62f01e60798e2e5ed05108f5fbbe1cca03fbcc37903cf7c5e3b

USER root

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl libopencv-dev unzip \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /opt/yolo \
    && curl -fL "${YOLO_MODEL_URL}" -o /opt/yolo/yolo11n.onnx \
    && echo "${YOLO_MODEL_SHA256}  /opt/yolo/yolo11n.onnx" | sha256sum -c -

# ONNX Runtime comes from the Microsoft.ML.OnnxRuntime NuGet package, which
# ships the Linux x64 and arm64 shared libraries under runtimes/<rid>/native.
RUN if [ "${TARGETARCH}" = "amd64" ]; then \
        rid=linux-x64; \
    elif [ "${TARGETARCH}" = "arm64" ]; then \
        rid=linux-arm64; \
    else \
        echo "Unsupported architecture: ${TARGETARCH}" >&2; exit 1; \
    fi \
    && curl -fL "https://api.nuget.org/v3-flatcontainer/microsoft.ml.onnxruntime/${ONNXRUNTIME_VERSION}/microsoft.ml.onnxruntime.${ONNXRUNTIME_VERSION}.nupkg" -o /tmp/onnxruntime.nupkg \
    && echo "${ONNXRUNTIME_NUPKG_SHA256}  /tmp/onnxruntime.nupkg" | sha256sum -c - \
    && mkdir -p /opt/onnxruntime/lib \
    && unzip -p /tmp/onnxruntime.nupkg "runtimes/${rid}/native/libonnxruntime.so" > "/opt/onnxruntime/lib/libonnxruntime.so.${ONNXRUNTIME_VERSION}" \
    && unzip -o -q /tmp/onnxruntime.nupkg LICENSE ThirdPartyNotices.txt -d /opt/onnxruntime \
    && ln -s "libonnxruntime.so.${ONNXRUNTIME_VERSION}" /opt/onnxruntime/lib/libonnxruntime.so.1 \
    && ln -s libonnxruntime.so.1 /opt/onnxruntime/lib/libonnxruntime.so \
    && rm /tmp/onnxruntime.nupkg

ENV YOLO_MODEL=/opt/yolo/yolo11n.onnx
ENV ONNXRUNTIME_SHARED_LIBRARY=/opt/onnxruntime/lib/libonnxruntime.so.1.31.0

USER vscode
