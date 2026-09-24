# Augment Vision Workflow Exercise

This repository demonstrates a complete custom workflow stage for Augment
Vision:

```text
Camera -> Agent -> Hub -> RabbitMQ -> Go stage -> Hub callback
                                      |
                                      +-> YOLO person detection
                                      +-> timeline marker
                                      +-> recording description
```

The stage is implemented in Go. It downloads the recording from the signed URL
in a workflow invocation, runs YOLO11n person detection, and submits the result
to Hub.

## Current status

The processing path works end to end:

- Agent recordings arrive in Hub.
- Hub dispatches workflow invocations through RabbitMQ.
- The worker downloads the signed media URL.
- GoCV decodes and samples the video.
- YOLO11n runs through the Go ONNX Runtime binding.
- A detected person produces:
  - a timeline `marker`; and
  - a `media-patch` that updates the recording description.
- A recording without a person returns a routing result and does not modify the
  recording.
- Hub accepts the bearer-authenticated callback and resolves the workflow
  stage.

The remaining platform feature is the **visual workflow editor**. Until it is
available, a facilitator must provision the workflow, RabbitMQ routing, queue,
and operation ID.

## Prerequisites

- Docker
- VS Code with the Dev Containers extension
- A Hub account
- An Agent public access key and private key
- An H.264 or H.265 RTSP camera stream
- A facilitator-provided RabbitMQ queue and operation ID
- A Hub token with the `workflow-run.update` scope

The dev container supports `amd64` and `arm64`. It installs Go, OpenCV, ONNX
Runtime, and the checksum-verified YOLO11n model.

## 1. Connect an Agent

In Hub, open **Devices**, select **Add Agent**, and collect:

- the Hub API URI;
- the public access key;
- the private key; and
- the site ID, when required.

Start an Agent after replacing the placeholders:

```bash
docker run -d --name introduction-agent --restart unless-stopped \
  -p 8080:80 \
  -e AGENT_NAME=introduction-agent \
  -e AGENT_KEY=introduction-agent-<your-name> \
  -e AGENT_CAPTURE_IPCAMERA_RTSP='******camera/stream' \
  -e AGENT_CAPTURE_CONTINUOUS=true \
  -e AGENT_CLOUD=s3 \
  -e AGENT_HUB_URI='https://api.example.com' \
  -e AGENT_HUB_KEY='<public-access-key>' \
  -e AGENT_HUB_PRIVATE_KEY='<private-key>' \
  kerberos/agent:latest
```

Add `-e AGENT_HUB_SITE='<site-id>'` when a site is assigned. Use a unique
`AGENT_KEY`; Hub uses it as the stable device identity.

Open [http://localhost:8080](http://localhost:8080). The default local Agent
credentials are `root` / `root`; change them before exposing the UI outside
your machine.

Verify that:

1. the camera preview works;
2. the Agent appears online in Hub; and
3. a recording made by the Agent is playable in Hub.

See the
[Agent installation tutorial](https://doc.kerberos.io/tutorials/agent/install/)
and
[camera connection tutorial](https://doc.kerberos.io/tutorials/agent/connect-any-camera/)
for additional setup options.

## 2. Configure workflow routing

The intended experience is to use a visual editor to select a device, add the
custom stage, and connect its outputs. That editor is not yet available.

For now, ask the facilitator to provide:

- a RabbitMQ host, port, scheme, username, password, and virtual host;
- an existing queue that receives workflow invocations;
- the stage operation assigned to that queue; and
- a workflow configured to dispatch recordings from your Agent.

The worker deliberately does not create queues, exchanges, or bindings. It
requires read access to the assigned queue but no broker management or publish
permissions. Use a dedicated queue per participant: multiple consumers on one
queue compete for deliveries.

## 3. Open the development environment

Open this repository as the VS Code folder and run:

**Dev Containers: Rebuild and Reopen in Container**

Use **Rebuild**, not only **Reopen**, after pulling changes to the
[dev-container Dockerfile](.devcontainer/Dockerfile). The build installs:

- OpenCV 4.6 for video decoding;
- ONNX Runtime 1.29;
- the official YOLO11n ONNX model; and
- Go module dependencies.

The model and runtime downloads are pinned by SHA-256 checksums.

The container does not start an Agent, RabbitMQ, or a background worker. Run
the Agent on the Docker host and start the worker explicitly.

Running outside the dev container requires Go 1.25.8+, OpenCV 4.6, ONNX Runtime
1.29, and a compatible YOLO11n ONNX model. The dev container is the recommended
environment.

## 4. Configure the stage

Copy the example environment file:

```bash
cp .env.example .env
```

The `.env` file is ignored by Git. Never commit it or include its values in
logs, screenshots, or support messages.

| Variable | Description |
| --- | --- |
| `RABBITMQ_SCHEME` | `amqps` for TLS; use `amqp` only on an approved trusted network |
| `RABBITMQ_HOST` | Broker hostname and port |
| `RABBITMQ_USERNAME`, `RABBITMQ_PASSWORD` | Broker credentials |
| `RABBITMQ_VHOST` | Assigned virtual host, commonly `/` |
| `STAGE_QUEUE` | Existing queue that receives workflow invocations |
| `STAGE_OPERATION` | Operation expected in `stage.operation`; defaults to `forwarder` |
| `MARKER_START_TIMESTAMP` | Recording start in Unix seconds |
| `MARKER_DURATION_SECONDS` | Marker interval in seconds; `1..3600`, default `30` |
| `YOLO_MODEL` | YOLO11n ONNX model path |
| `ONNXRUNTIME_SHARED_LIBRARY` | ONNX Runtime shared-library path |
| `YOLO_CONFIDENCE` | Person confidence threshold in `(0, 1]`; default `0.5` |
| `YOLO_SAMPLE_FPS` | Frames analyzed per second in `(0, 30]`; default `1` |
| `RESULT_MODE` | `preview`, `bypass`, or `http` |
| `WORKFLOW_BEARER_TOKEN` | Hub token with `workflow-run.update`; required in HTTP mode |
| `LOG_LEVEL` | Logrus level; default `info` |
| `LOG_OUTPUT` | `text` or `json`; default `text` |

Use the recording timestamp supplied for the test. The current example applies
one configured interval to the detected-person marker; it does not yet derive
entry and exit timestamps from a person track.

## 5. Run the stage

VS Code launch profiles are defined in
[.vscode/launch.json](.vscode/launch.json).

### Offline fixture

Replace the placeholder `media.signedUrl` in
[examples/workflow-invocation.json](examples/workflow-invocation.json) with a
reachable HTTP or HTTPS video URL no larger than 100 MiB.

Select **Stage: offline sample** in **Run and Debug**, or run:

```bash
MARKER_START_TIMESTAMP=1700000000 \
  go run . -input examples/workflow-invocation.json
```

Offline input always prints a local preview. It does not consume RabbitMQ or
call Hub.

### Queue preview

Select **Stage: queue preview**, or run:

```bash
set -a
. ./.env
set +a
go run .
```

Preview mode:

1. receives one invocation;
2. downloads and analyzes the recording;
3. prints the generated result;
4. requeues the RabbitMQ delivery; and
5. exits without calling Hub.

Preview is not a broker peek. It changes redelivery state and may count toward
a configured delivery limit.

### Queue bypass

Select **Stage: queue bypass** or set `RESULT_MODE=bypass`.

Bypass analyzes and prints each result, acknowledges the delivery, and does not
call Hub. It permanently removes successful messages from the queue without
resolving their workflow stages. Use it only for deliberate queue draining.

### HTTP callback

Set `WORKFLOW_BEARER_TOKEN`, select **Stage: HTTP callback**, and trigger a new
recording or re-analysis.

The worker calls the run-specific endpoint from the invocation:

```http
POST /workflows/runs/{runId}
Authorization: Bearer <token>
Content-Type: application/json
Idempotency-Key: <runId>:<operation>
```

Use `-once` to stop after one acknowledged HTTP delivery:

```bash
go run . -once
```

## Processing behavior

For each valid invocation, the worker:

1. validates the schema, run ID, stage operation, callback, media key, and
   marker interval;
2. downloads `media.signedUrl` into memory with a two-minute timeout and a
   100 MiB limit;
3. stages the bytes in a private temporary file because OpenCV video capture
   requires a filename;
4. samples frames at `YOLO_SAMPLE_FPS`;
5. runs YOLO11n until the first person confidence reaches
   `YOLO_CONFIDENCE`; and
6. submits one of the two result forms below.

The temporary file is removed after processing. Signed URL credentials and
query parameters are redacted from logs.

### Person detected

Hub receives an ingest `payload` containing a marker and media patch:

```json
{
  "schema": "uug.ai/workflow-result/v1",
  "stage": {
    "operation": "forwarder"
  },
  "payload": {
    "blocks": [
      {
        "type": "marker",
        "data": {
          "siteId": "site-1",
          "organisationId": "org-1",
          "mediaKeys": ["recording.mp4"],
          "startTimestamp": 1700000000,
          "endTimestamp": 1700000030,
          "duration": 30,
          "name": "person detected",
          "description": "YOLO detected a person with 0.91 confidence after analyzing 3 sampled frames"
        }
      },
      {
        "type": "media-patch",
        "data": {
          "mediaKey": "recording.mp4",
          "description": "YOLO detected a person with 0.91 confidence after analyzing 3 sampled frames"
        }
      }
    ]
  }
}
```

The marker is linked directly to the recording through `mediaKeys`. The media
patch updates the recording description with the same deterministic detection
summary.

### No person detected

An ingest payload may not contain an empty block list. When no person meets the
threshold, the worker therefore sends a routing `result` instead:

```json
{
  "schema": "uug.ai/workflow-result/v1",
  "stage": {
    "operation": "forwarder"
  },
  "result": {
    "framesAnalyzed": 30,
    "personDetected": false
  }
}
```

This resolves the workflow stage without creating a marker or changing the
recording description.

## Delivery and retry semantics

- RabbitMQ uses explicit acknowledgements with a prefetch of one.
- Preview always requeues its delivery.
- HTTP mode acknowledges only after a `2xx` callback response.
- Timeouts, redirects, authorization errors, and other non-`2xx` responses
  requeue the delivery and stop the worker.
- Malformed invocations and processing failures also requeue and stop, avoiding
  an immediate retry loop.
- Ctrl+C and broker disconnections leave unacknowledged deliveries for
  RabbitMQ recovery.
- Callback redirects are not followed.
- Callback HTTP is allowed only for loopback testing; other endpoints require
  HTTPS.

If an invocation omits `callback.url`, HTTP mode prints the result and
acknowledges the delivery, but the workflow stage remains unresolved.

## Security

- Keep Agent, RabbitMQ, storage, and Hub credentials out of Git.
- Use a Hub token scoped only to `workflow-run.update`.
- Use TLS for RabbitMQ and callbacks outside an approved local test network.
- Do not disable certificate validation.
- Signed URLs, embedded URL credentials, tokens, passwords, storage fields,
  and private keys are redacted from structured invocation logs.
- Callback bodies are rebuilt from validated fields instead of echoing the
  invocation.
- Media downloads are bounded to 100 MiB.
- The model and ONNX Runtime archives are checksum verified during the
  dev-container build.

## Project layout

| Path | Purpose |
| --- | --- |
| [main.go](main.go) | Executable entry point |
| [internal/app/worker.go](internal/app/worker.go) | RabbitMQ consumption and acknowledgement lifecycle |
| [internal/app/persondetection.go](internal/app/persondetection.go) | Invocation validation, media download, and result construction |
| [internal/app/yolo.go](internal/app/yolo.go) | GoCV video sampling and Go ONNX inference |
| [internal/app/support.go](internal/app/support.go) | Configuration, callback validation, and HTTP submission |
| [internal/app/logging.go](internal/app/logging.go) | Structured logging and credential redaction |
| [examples/workflow-invocation.json](examples/workflow-invocation.json) | Offline invocation fixture |
| [.devcontainer/Dockerfile](.devcontainer/Dockerfile) | OpenCV, ONNX Runtime, and YOLO model environment |

## Testing

Run:

```bash
go test ./...
go vet ./...
```

The test suite covers:

- invocation and callback validation;
- signed-media download behavior and limits;
- YOLO tensor preprocessing and person-score extraction;
- marker and media-patch payloads;
- no-person routing results;
- callback success and failure handling;
- RabbitMQ acknowledgement and requeue behavior; and
- credential redaction.

An opt-in integration test exercises real video decoding and YOLO inference:

```bash
YOLO_MODEL_INTEGRATION="$YOLO_MODEL" \
ONNXRUNTIME_SHARED_LIBRARY_INTEGRATION="$ONNXRUNTIME_SHARED_LIBRARY" \
  go test ./internal/app -run TestYOLOModelIntegration -v
```

## Remaining work

The backend stage is operational. The main missing platform feature is the
visual workflow editor for selecting sources, arranging stages, configuring
operations and queues, and activating routing without facilitator assistance.

Further stage improvements could include:

- person tracking across frames;
- exact entry and exit timestamps;
- persisted bounding-box tracks;
- configurable marker descriptions; and
- production tuning for camera placement, confidence, and sampling rate.

Useful references:

- [Custom workflow stage tutorial](https://learn.uug.ai/tutorials/hub/custom-workflow-stage/)
- [Marker contract](https://learn.uug.ai/docs/hub/workflows/ingest/blocks/marker/)
- [Production reference service](https://github.com/uug-ai/hub-loitering)

## Completion checklist

- [ ] Agent camera preview works.
- [ ] Agent is online in Hub.
- [ ] A recording is playable in Hub.
- [ ] Dev container has been rebuilt successfully.
- [ ] `go test ./...` passes.
- [ ] Queue preview analyzes and requeues a real invocation.
- [ ] Hub token has only `workflow-run.update`.
- [ ] HTTP callback returns `2xx`.
- [ ] A person detection creates a marker and updates the recording description.
- [ ] A no-person result resolves the workflow without changing the recording.
- [ ] Visual workflow editor is available; until then, routing is provisioned manually.
