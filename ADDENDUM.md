# Exercise addendum: technical reference

This is optional reference material for the
[Augment Vision workflow exercise](README.md). Complete the baseline, inspect
what happens, and develop your own explanation and extension first. Use this
page when you need a contract example, want to investigate the implementation,
or are troubleshooting a specific problem.

- [Processing behavior and result examples](#processing-behavior)
- [Delivery and retry semantics](#delivery-and-retry-semantics)
- [Security](#security)
- [Forwarder configuration details](#forwarder-configuration-details)
- [Advanced stage settings](#advanced-stage-settings)
- [Command-line HTTP callback](#command-line-http-callback)
- [Troubleshooting](#troubleshooting)
- [Project layout](#project-layout)
- [Testing](#testing)

## Processing behavior

For each valid invocation, the worker:

1. validates the schema, run ID, stage operation, callback, media key, and
   marker interval
2. downloads `media.signedUrl` into memory with a two-minute timeout and a
   100 MiB limit
3. stages the bytes in a private temporary file because OpenCV video capture
   requires a filename
4. samples frames at `YOLO_SAMPLE_FPS`
5. runs YOLO11n until the first person confidence reaches
   `YOLO_CONFIDENCE`
6. submits one of the two result forms below

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

- RabbitMQ uses explicit acknowledgements with a prefetch of one
- HTTP mode acknowledges only after a `2xx` callback response
- Timeouts, redirects, authorization errors, and other non-`2xx` responses
  requeue the delivery and stop the worker
- Malformed invocations and processing failures also requeue and stop, avoiding
  an immediate retry loop
- Ctrl+C and broker disconnections leave unacknowledged deliveries for
  RabbitMQ recovery
- Callback redirects are not followed
- Callback HTTP is allowed only for loopback testing; other endpoints require
  HTTPS

If an invocation omits `callback.url`, HTTP mode prints the result and
acknowledges the delivery, but the workflow stage remains unresolved.

## Security

- Keep Agent, RabbitMQ, storage, and Hub credentials out of Git
- Use a Hub token scoped only to `workflow-runs.update`
- Use TLS for RabbitMQ and callbacks outside an approved local test network
- Do not disable certificate validation
- Signed URLs, embedded URL credentials, tokens, passwords, storage fields
  and private keys are redacted from structured invocation logs
- Callback bodies are rebuilt from validated fields instead of echoing the
  invocation
- Media downloads are bounded to 100 MiB
- The model and ONNX Runtime archives are checksum verified during the
  dev-container build

## Forwarder configuration details

The learner obtains destination settings and queue provisioning instructions
from the tutor. These details explain how those settings map to the worker.

| Forwarder setting | Exercise setting | Corresponding worker setting |
| --- | --- | --- |
| **Provider** | **RabbitMQ** (`broker=rabbitmq`) | The worker consumes RabbitMQ |
| **Completion mode** | **Wait for result** (`mode=callback`), not **Complete after delivery** | `RESULT_MODE=http` for the final run |
| **Maximum retries** | Keep `5` for the baseline; this limits retries of failed forwarder delivery, not worker processing | No matching worker setting |
| **Host**, **Port** | Destination broker hostname and port, e.g. `broker.example.com`, `5671` | `RABBITMQ_HOST=broker.example.com:5671` |
| **Queue** | Destination queue, e.g. `person-detection-exercise` | `STAGE_QUEUE=person-detection-exercise` |
| **Virtual host** | Destination virtual host, commonly `/`; blank uses `/`, not the deployment virtual host | `RABBITMQ_VHOST=/` |
| **Use TLS** | Enabled outside an approved trusted local network | `RABBITMQ_SCHEME=amqps` |
| **Declare destination queues** | Enabled if the forwarder's credentials may create durable quorum queues; otherwise have the administrator provision both queues first | The worker never declares queues |
| **Destination dead-letter queue** | A different queue from **Queue**; blank uses `<queue>.forwarder-dead-letter` | No matching worker setting |
| **Username**, **Password** | Your destination credentials, permitted to publish and declare/check queues as configured; deployment credentials are never used | `RABBITMQ_USERNAME`, `RABBITMQ_PASSWORD`: credentials permitted to consume |

The forwarder and worker must use the same broker, queue, virtual host, and TLS
configuration. They can use separate publisher and consumer credentials.
`STAGE_OPERATION=forwarder` matches the operation, not the workflow name or
block label.

In **Wait for result** (`callback`) mode, Hub waits for your stage to return
its result. In **Complete after delivery** (`delivered`) mode, forwarding the
message completes the stage immediately and the invocation has no callback
block, so it cannot demonstrate this exercise's result-ingestion loop.
The administrator must also configure `FORWARDER_CALLBACK_BASE_URL` to the
public HTTPS Hub API base URL; you do not enter your Hub access token in the
forwarder block.

Do not use Hub's internal engine/stage queue as the external destination. The
worker consumes the safe, versioned invocation from **your destination queue**,
not an internal `WorkflowRun`. It deliberately does not create queues, exchanges,
or bindings, and requires consumer read access but no broker management or
publish permissions. If **Declare destination queues** is disabled, both the
destination and destination dead-letter queue must already exist. If enabled,
the forwarder creates durable quorum queues when missing; existing queues must
be compatible with those declarations. Provision them with the administrator
or use credentials allowed to declare them.

For a new custom destination, **Declare destination queues** takes effect when
the forwarder dispatches the first invocation, not when you save the workflow.
The worker cannot start consuming a nonexistent queue.

The destination dead-letter queue setting only creates or checks that queue.
It does not automatically route failed external-consumer messages there;
configure dead-letter routing separately on the broker or consumer. Never use
Hub's internal failed-dispatch queue as the external dead-letter destination.

If the forwarder is missing from **Blocks**, the administrator must enable the
service and expose its operation in the workflow catalog. A deployment-managed
workflow opens read-only; duplicate it to create an editable workflow instead.
See the [external-stage reference](https://learn.uug.ai/docs/hub/workflows/external-stages/)
for deployment configuration and contracts.

## Advanced stage settings

Keep the example defaults for the baseline. Consult these settings when
investigating or extending the stage:

| Variable | Description |
| --- | --- |
| `STAGE_OPERATION` | Operation expected in `stage.operation`; defaults to `forwarder` |
| `YOLO_MODEL` | YOLO11n ONNX model path |
| `ONNXRUNTIME_SHARED_LIBRARY` | ONNX Runtime shared-library path |
| `YOLO_CONFIDENCE` | Person confidence threshold in `(0, 1]`; default `0.5` |
| `YOLO_SAMPLE_FPS` | Frames analyzed per second in `(0, 30]`; default `1` |
| `LOG_LEVEL` | Logrus level; default `info` |
| `LOG_OUTPUT` | `text` or `json`; default `text` |

The [dev-container Dockerfile](.devcontainer/Dockerfile) installs OpenCV 4.6,
ONNX Runtime 1.29, and the official YOLO11n ONNX model. The model and runtime
downloads are pinned by SHA-256 checksums.

Running outside the dev container requires Go 1.25.8+, OpenCV 4.6, ONNX Runtime
1.29, and a compatible YOLO11n ONNX model. The dev container is the recommended
environment.

## Command-line HTTP callback

First complete the [queue and recording preparation](README.md#6-run-the-stage).
Run commands from the repository root inside the development container.
Set `RESULT_MODE=http` in `.env`, then load it and run one delivery:

```bash
set -a
. ./.env
set +a
go run . -once
```

The VS Code HTTP callback profile reads environment-file values; these CLI
commands **execute the file as shell input**. With single quotes, dollar signs,
spaces, and `#` remain literal in both paths. Keep the shared file to
assignments and comments, not commands or shell quote-concatenation expressions.

If a password contains a literal single quote, leave `RABBITMQ_PASSWORD=''`
in the shared file and use the CLI path. After loading the file, but before
`go run`, enter the password without echoing it:

```bash
read -r -s -p 'RabbitMQ password: ' RABBITMQ_PASSWORD
printf '\n'
export RABBITMQ_PASSWORD
```

This Bash prompt preserves the entered characters without evaluating them.
It is CLI-only: a VS Code profile does not receive this shell's override.
Use the same approach for another credential if needed, with its variable name.
Enter the override again whenever you reload `.env`.

The worker calls the run-specific endpoint from the invocation:

```http
POST /workflows/runs/{runId}
Authorization: Bearer <token>
Content-Type: application/json
Idempotency-Key: <runId>:<operation>
```

## Troubleshooting

| Symptom | Check |
| --- | --- |
| Agent is online but no recording appears in Hub | Verify the local camera preview and a playable clip under Agent **Recordings**. Confirm the [ROI is saved](README.md#configure-a-region-of-interest-roi); motion-based capture needs activity inside it, whereas continuous capture does not. Check recording conditions, cloud settings, and capture/upload errors in `docker logs --since 5m --tail 100 introduction-agent` on the Docker host. Redact credentials, tokens, and signed URLs before sharing logs. |
| No workflow run appears | Workflow is enabled and saved; automatic start is connected; the connection selects your Agent and uses an active schedule; trigger a fresh recording or re-analysis. |
| A run appears but nothing reaches your queue | Forwarder service, catalog operation, destination credentials, broker reachability, queue, virtual host, and TLS settings. |
| Worker says it cannot consume `STAGE_QUEUE` | Confirm the queue exists in that virtual host and the consumer has read permission. For a new destination with declaration enabled, first trigger the forwarder's delivery as described in the README's queue preparation. |
| Invocation has no `callback.url` | Forwarder mode must be `callback`, with the public Hub API base URL configured. Do not use this invocation to verify callback completion. |
| Callback returns `401` or `403` | Raw token has no `Bearer ` prefix, is unexpired, belongs to the correct tenant, and has exactly `workflow-runs.update`. |
| Callback returns `409` | A different result already completed that run/stage; use a fresh run rather than changing an already accepted result. |
| Worker cannot download the recording | Signed URL is reachable from the dev container and has not expired; re-analysis creates a fresh invocation. |
| Marker appears at the wrong time | Copy `data.media.startTimestamp` for the queued test recording, confirm it is the only invocation in the queue, reload `.env`, and use a one-shot callback. The value is Unix seconds, not an offset or milliseconds. |

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

- invocation and callback validation
- signed-media download behavior and limits
- YOLO tensor preprocessing and person-score extraction
- marker and media-patch payloads
- no-person routing results
- callback success and failure handling
- RabbitMQ acknowledgement and requeue behavior
- credential redaction

An opt-in integration test exercises real video decoding and YOLO inference:

```bash
YOLO_MODEL_INTEGRATION="$YOLO_MODEL" \
ONNXRUNTIME_SHARED_LIBRARY_INTEGRATION="$ONNXRUNTIME_SHARED_LIBRARY" \
  go test ./internal/app -run TestYOLOModelIntegration -v
```

Run these tests from the repository root in the development container. Load
`.env` first to set the model/runtime paths for the opt-in integration test.
