# Augment Vision Workflow Exercise

## Goal of the exercise

The goal of this exercise is to help a learner understand the complete Augment
Vision product flow: how video is captured at the edge, stored and orchestrated
through Hub, processed by a custom machine-learning stage, and enriched with
structured feedback that makes recordings searchable and filterable.

By completing the exercise, the learner should be able to:

- explain how Agent, Hub, workflows, RabbitMQ, custom processing stages, and
  workflow callbacks fit together;
- configure and run a working end-to-end workflow;
- process a Hub recording with a custom ML model;
- return markers and media metadata to Hub;
- understand how those results support timeline review, search, and filtering;
  and
- identify the contracts, runtime behavior, and operational concerns needed to
  build production features at Augment Vision.

This is a guided but deliberately open-ended exercise. The repository already
contains a working person-detection example; the learner should first configure
it and verify the complete flow before changing it. The example is a starting
point, not the final assignment.

Once the baseline works, learners can adapt the processing logic to a use case
of their choice. For example, they might:

- count people in a recording;
- add a counting line and report how many people cross it;
- detect cars or another object class;
- identify a domain-specific event; or
- produce different markers and searchable recording metadata.

The important outcome is not one particular model. It is understanding how to
integrate custom computer-vision logic into the Augment Vision platform and
feed useful, structured results back into Hub.

## Working reference implementation

The included Go stage demonstrates the complete flow:

```text
Camera -> Agent -> Hub workflow -> Forwarder -> Your RabbitMQ -> Go stage
                                  callback mode                   |
Hub API <- authenticated workflow callback <---------------------+
   |
   +-> timeline marker
   +-> recording description
```

It downloads a recording from the signed URL in a workflow invocation, runs
YOLO11n person detection, and submits the result to Hub. The implementation is
ready to run once the Agent, workflow designer, forwarder destination,
recording interval, and Hub callback credentials are configured.

## Current status

The processing path works end to end:

- Agent recordings arrive in Hub.
- A workflow created in the designer selects recordings from your Agent.
- The forwarder dispatches external workflow invocations to your RabbitMQ queue.
- The worker downloads the signed media URL.
- GoCV decodes and samples the video.
- YOLO11n runs through the Go ONNX Runtime binding.
- A detected person produces:
  - a timeline `marker`
  - a `media-patch` that updates the recording description.
- A recording without a person returns a routing result and does not modify the
  recording.
- Hub accepts the bearer-authenticated callback and resolves the workflow
  stage.

The **visual workflow designer and forwarder block are available**. You create
and activate the workflow in Hub; an administrator still needs to enable the
workflow engine and forwarder service and make the forwarder operation available
in the deployment's block catalog. RabbitMQ access and the public callback base
URL are deployment prerequisites, not something the exercise worker provisions.

## Prerequisites

- Docker
- VS Code with the Dev Containers extension
- A Hub owner account for creating an access token, and permission to create
  workflows in the organisation/project containing your Agent
- An Agent public access key and private key
- An H.264 or H.265 RTSP camera stream
- A Hub deployment with the workflow designer and `forwarder` block enabled
- An existing RabbitMQ destination queue reachable by both the Hub forwarder
  and your development container, with credentials for each
- A publicly reachable HTTPS Hub API configured as the forwarder's callback base URL
- A Hub token with the `workflow-runs.update` scope (created in Step 3)

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

1. the camera preview works
2. the Agent appears online in Hub
3. a recording made by the Agent is playable in Hub

See the
[Agent installation tutorial](https://doc.kerberos.io/tutorials/agent/install/)
and
[camera connection tutorial](https://doc.kerberos.io/tutorials/agent/connect-any-camera/)
for additional setup options.

## 2. Design the workflow in Hub

### Create and connect the blocks

1. Select the organisation/project containing your Agent and open **Workflows**.
2. Select **Add Workflow**, give it a name such as `Person detection exercise`,
   and add a short description. Leave it **Disabled** while configuring it.
3. The new canvas already contains a start block. Select it and keep its start
   mode **Automatic** so new recordings can start the workflow.
4. Drag the forwarder block from **Blocks** onto the canvas to the right of
   the start block. Its display name is deployment-configurable; the operation
   must be `forwarder`.
5. Drag from the start block's right-hand output port to the forwarder block's
   left-hand input port. The connection determines execution order; placing
   blocks next to each other does not connect them.
6. Select the **connection line**, not the forwarder block, to configure the
   automatic trigger. Under **Devices**, select only your exercise Agent.
   Leave sites, groups, classifications, and additional conditions unset, and
   use **Always** for the schedule.

Device selection and scheduling belong to the connection leaving an automatic
start block. Leaving the device selection empty includes every device in scope.
Do not add a `person` classification filter for the baseline: the exercise's
own YOLO model should analyze both person and no-person recordings.

![Workflow design guide: automatic start connected to the forwarder, with device selection on the connection and callback settings on the forwarder](docs/images/workflow-designer.svg)

*Illustrated guide, not a Hub screenshot. The block name and available settings
depend on the deployment's catalog.*

### Configure the forwarder destination

Select the **forwarder block** to open its settings. For the environment-backed
RabbitMQ integration, configure the following parameters, or confirm that the
administrator has set equivalent deployment defaults:

| Forwarder parameter | Exercise setting | Corresponding worker setting |
| --- | --- | --- |
| `broker` | `rabbitmq` | The worker consumes RabbitMQ |
| `host`, `port` | Destination broker hostname and port, e.g. `broker.example.com`, `5671` | `RABBITMQ_HOST=broker.example.com:5671` |
| `username`, `password` | Credentials permitted to publish to the destination | `RABBITMQ_USERNAME`, `RABBITMQ_PASSWORD`: credentials permitted to consume |
| `queue` | Existing destination queue, e.g. `person-detection-exercise` | `STAGE_QUEUE=person-detection-exercise` |
| `vhost` | Destination virtual host, commonly `/` | `RABBITMQ_VHOST=/` |
| `tls` | Enabled outside an approved trusted local network | `RABBITMQ_SCHEME=amqps` |
| `mode` | **`callback`**, not `delivered` | `RESULT_MODE=http` for the final run |

The forwarder and worker must use the same broker, queue, virtual host, and TLS
configuration. They can use separate publisher and consumer credentials.
`STAGE_OPERATION=forwarder` matches the operation, not the workflow name or
block label.

In **callback** mode, Hub waits for your stage to return its result. In
**delivered** mode, forwarding the message completes the stage immediately and
the invocation has no callback block, so it cannot demonstrate this exercise's
result-ingestion loop. The administrator must also configure
`FORWARDER_CALLBACK_BASE_URL` to the public HTTPS Hub API base URL; you do not
enter your Hub access token in the forwarder block.

Do not use Hub's internal engine/stage queue as the external destination. The
worker consumes the safe, versioned invocation from **your destination queue**,
not an internal `WorkflowRun`. It deliberately does not create queues, exchanges,
or bindings, and requires consumer read access but no broker management or
publish permissions. Have the administrator provision the destination or enable
the forwarder's queue declaration before continuing.

Select **Create workflow** to save the disabled draft. No downstream block is
needed: returning the result to Hub completes this single-stage workflow.
Enable it and select **Save changes** only when the worker is configured and
you are ready to test in Step 6.

If the forwarder is missing from **Blocks**, the administrator must enable the
service and expose its operation in the workflow catalog. A deployment-managed
workflow opens read-only; duplicate it to create an editable workflow instead.
See the [external-stage reference](https://learn.uug.ai/docs/hub/workflows/external-stages/)
for deployment configuration and contracts.

## 3. Create the Hub access token

The callback token is separate from your Agent keys and RabbitMQ credentials.
Create it for the owner whose organisation/project contains the workflow and
recording.

1. Open your Hub **Profile** page (`/profile`) and select **Access tokens**.
   This section is currently shown for owner accounts. If it is absent, have
   the appropriate owner create a narrowly scoped token for the exercise.
2. Select **Generate access token**.
3. In **Details**, enter a name such as `person-detection-exercise` (at least
   three characters) and a description such as `Return person-detection workflow results`.
   Both are required.
4. In **Expiry**, choose a short lifetime that covers the exercise, such as
   **24 hours**, or select an exact expiry date.
5. In **Permissions**, select the **`workflow-runs`** area and check only
   **`update`**. Confirm that **Assigned permissions** contains exactly
   **`workflow-runs.update`**. Do not select all permissions.
6. Select **Generate** and copy the token from the success dialog before closing
   it. Store it privately; treat it as a secret.

![Access-token guide showing the Details, Expiry, and Permissions tabs, the single workflow-runs.update permission, and removal of the Bearer prefix for the worker](docs/images/access-token.svg)

*Illustrated guide, not a Hub screenshot. No real token is shown.*

The dialog's copy control includes the `Bearer ` prefix for use in an HTTP
Authorization header. **Remove that prefix** when setting
`WORKFLOW_BEARER_TOKEN`: the exercise worker adds it itself. In your ignored
`.env` file, use only the raw token:

```dotenv
WORKFLOW_BEARER_TOKEN='<raw-token-without-Bearer-prefix>'
```

The permission is plural: `workflow-runs.update`, not `workflow-run.update`.
The token must belong to the correct tenant, be unexpired, and authorize the
run being completed. Do not put it in the workflow settings, commit it, or
include it in screenshots. Delete the exercise token from **Access tokens**
when finished; if it is exposed, revoke or rotate it before continuing.

## 4. Open the development environment

Open this repository as the VS Code folder and run:

**Dev Containers: Rebuild and Reopen in Container**

Use **Rebuild**, not only **Reopen**, after pulling changes to the
[dev-container Dockerfile](.devcontainer/Dockerfile). The build installs:

- OpenCV 4.6 for video decoding
- ONNX Runtime 1.29
- the official YOLO11n ONNX model
- Go module dependencies

The model and runtime downloads are pinned by SHA-256 checksums.

The container does not start an Agent, RabbitMQ, or a background worker. Run
the Agent on the Docker host and start the worker explicitly.

Running outside the dev container requires Go 1.25.8+, OpenCV 4.6, ONNX Runtime
1.29, and a compatible YOLO11n ONNX model. The dev container is the recommended
environment.

## 5. Configure the stage

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
| `WORKFLOW_BEARER_TOKEN` | Raw Hub token with `workflow-runs.update`, without the `Bearer ` prefix; required in HTTP mode |
| `LOG_LEVEL` | Logrus level; default `info` |
| `LOG_OUTPUT` | `text` or `json`; default `text` |

Use the recording timestamp supplied for the test. The current example applies
one configured interval to the detected-person marker; it does not yet derive
entry and exit timestamps from a person track.

## 6. Run the stage

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

Start the preview worker first, then enable and save your workflow in Hub and
trigger a new recording or re-analysis from the selected Agent. This supplies
a real invocation to the destination queue; a disabled workflow does not
dispatch new work.

Preview mode:

1. receives one invocation
2. downloads and analyzes the recording
3. prints the generated result
4. requeues the RabbitMQ delivery
5. exits without calling Hub

Preview is not a broker peek. It changes redelivery state and may count toward
a configured delivery limit.

### Queue bypass

Select **Stage: queue bypass** or set `RESULT_MODE=bypass`.

Bypass analyzes and prints each result, acknowledges the delivery, and does not
call Hub. It permanently removes successful messages from the queue without
resolving their workflow stages. Use it only for deliberate queue draining.

### HTTP callback

Set `WORKFLOW_BEARER_TOKEN` to the raw token from Step 3. Select **Stage: HTTP
callback** to start the worker, then open your saved workflow, switch it to
**Enabled**, and select **Save changes**. Trigger a new recording or re-analysis
from your selected Agent.

For the command-line equivalent, set `RESULT_MODE=http` in `.env`, load it using
the commands shown under Queue preview, and run `go run .`. The VS Code HTTP
callback profile sets that mode automatically; the file defaults to `preview`.

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

## 7. Verify the result in Hub

1. Open **Workflows**, find your workflow, and select **View runs**, or open
   **Runs** and filter to it.
2. Open the run for your test recording. Confirm that the `forwarder` stage
   resolves after the worker's callback succeeds, rather than immediately after
   queue delivery.
3. Open the recording in Hub. For a clip containing a person, verify that the
   `person detected` marker appears at your configured interval and that the
   recording description contains the YOLO detection summary.
4. Repeat with a recording without a person. The run should finish, but no
   marker or recording-description change should be created by this stage.

Preview mode deliberately leaves the stage waiting: it never calls Hub.
After previewing an invocation, process that requeued delivery in HTTP mode
while its signed media URL and callback run remain valid, or create a fresh run.
Adjust `MARKER_START_TIMESTAMP` to match the recording being processed.

| Symptom | Check |
| --- | --- |
| No workflow run appears | Workflow is enabled and saved; automatic start is connected; the connection selects your Agent and uses an active schedule; trigger a fresh recording or re-analysis. |
| A run appears but nothing reaches your queue | Forwarder service, catalog operation, destination credentials, broker reachability, queue, virtual host, and TLS settings. |
| Invocation has no `callback.url` | Forwarder mode must be `callback`, with the public Hub API base URL configured. Do not use this invocation to verify callback completion. |
| Callback returns `401` or `403` | Raw token has no `Bearer ` prefix, is unexpired, belongs to the correct tenant, and has exactly `workflow-runs.update`. |
| Callback returns `409` | A different result already completed that run/stage; use a fresh run rather than changing an already accepted result. |
| Worker cannot download the recording | Signed URL is reachable from the dev container and has not expired; re-analysis creates a fresh invocation. |
| Marker appears at the wrong time | `MARKER_START_TIMESTAMP` is the recording start in Unix seconds, not an offset or milliseconds. |

Disable and save the exercise workflow when finished so new recordings no
longer queue work for an unattended worker. Delete the exercise access token.

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
- Preview always requeues its delivery
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

## Extend the exercise

The baseline now includes designing the workflow, configuring the forwarder,
creating the callback token, and verifying the result in Hub. Once it works,
further stage improvements could include:

- person tracking across frames
- exact entry and exit timestamps
- persisted bounding-box tracks
- configurable marker descriptions
- production tuning for camera placement, confidence, and sampling rate

Useful references:

- [Custom workflow stage tutorial](https://learn.uug.ai/tutorials/hub/custom-workflow-stage/)
- [Marker contract](https://learn.uug.ai/docs/hub/workflows/ingest/blocks/marker/)
- [Production reference service](https://github.com/uug-ai/hub-loitering)

## Completion checklist

- [ ] Agent camera preview works.
- [ ] Agent is online in Hub.
- [ ] A recording is playable in Hub.
- [ ] Workflow connects an automatic start block to the `forwarder` block.
- [ ] The start connection selects only the exercise Agent and uses an active schedule.
- [ ] Forwarder uses the correct external destination queue and `callback` mode.
- [ ] Workflow is saved and enabled for the test.
- [ ] Dev container has been rebuilt successfully.
- [ ] `go test ./...` passes.
- [ ] Queue preview analyzes and requeues a real invocation.
- [ ] Hub token has only `workflow-runs.update`; `.env` contains the raw token.
- [ ] HTTP callback returns `2xx`.
- [ ] The run's `forwarder` stage resolves in Hub.
- [ ] A person detection creates a marker and updates the recording description.
- [ ] A no-person result resolves the workflow without changing the recording.
- [ ] Exercise workflow is disabled and the exercise token is deleted after testing.
