# Getting acquinted with Augment Vision

This exercise introduces the core products of Augment Vision through one complete media flow:

```text
Camera -> Agent -> Hub -> Workflow -> Your stage -> Marker -> Hub timeline
```

There are two parts: install an Agent and link it to Hub, then configure a
workflow and run your own stage. This repository includes a runnable Go stage
based on the [custom workflow stage tutorial](https://learn.uug.ai/tutorials/hub/custom-workflow-stage/).

Allow about 60-90 minutes. Today, messages are already landing in a test
RabbitMQ. You can consume a real dispatch and preview a synthetic marker now.
Workflow configuration through the UI and the bearer-authenticated result API
are still to be developed; completing the full loop in Hub depends on those.

## Prerequisites

- A Hub account and Agent connection credentials
- An H.264 or H.265 RTSP camera stream
- Docker on your host for the Agent and dev container
- VS Code with the Dev Containers extension, or Go 1.25+ installed locally
- From the facilitator: test RabbitMQ host/port, TLS setting, username,
  password, virtual host, assigned queue name, and stage operation ID
- Later: a workflow-scoped bearer token for the invocation's callback URL

You do not need Kubernetes, Helm, MongoDB access, or a container registry for
this exercise. The facilitator manages the workflow and test broker.

Do not commit camera passwords, Hub credentials, broker credentials, or storage
credentials to this repository.

## Part 1: Connect an Agent to Hub

An Agent runs close to one camera. It reads the camera stream, creates
recordings, and sends them to Hub for central processing and review.

### 1. Register and get the Hub credentials

First, register your Hub account. After registration, ask your tutor to arrange
a subscription and provide your Hub public access key and Hub private key. You
cannot proceed with this exercise until you have received both keys.

Then sign in to Hub, open **Devices**, and select **Add Agent**. Collect the
values needed to connect your Agent:

- Hub API URI
- Public access key (Hub key)
- Private key (Hub private key)
- Site ID, if your environment requires one

### 2. Start an Agent

Replace the placeholders and run:

```bash
docker run -d --name introduction-agent --restart unless-stopped \
  -p 8080:80 \
  -e AGENT_NAME=introduction-agent \
  -e AGENT_KEY=introduction-agent-<your-name> \
  -e AGENT_CAPTURE_IPCAMERA_RTSP='rtsp://user:password@camera/stream' \
  -e AGENT_CAPTURE_CONTINUOUS=true \
  -e AGENT_CLOUD=s3 \
  -e AGENT_HUB_URI='https://api.example.com' \
  -e AGENT_HUB_KEY='<public-access-key>' \
  -e AGENT_HUB_PRIVATE_KEY='<private-key>' \
  kerberos/agent:latest
```

Add `-e AGENT_HUB_SITE='<site-id>'` when a site was assigned to you. Use a
unique `AGENT_KEY`; Hub uses it as the stable device identity.

Open [http://localhost:8080](http://localhost:8080). The default local Agent
credentials are `root` / `root`; change them before exposing the UI outside
your machine.

### 3. Verify the connection

1. Confirm that the camera preview works in the Agent UI.
2. Check the container logs with `docker logs -f introduction-agent`.
3. Find `introduction-agent` on the Hub **Devices** page and confirm it is
   online.
4. Trigger a recording and confirm that it appears in Hub.

Part 1 is complete when you can play an Agent recording in Hub.

For other installation methods and camera troubleshooting, use the
[Agent installation tutorial](https://doc.kerberos.io/tutorials/agent/install/)
and [camera connection tutorial](https://doc.kerberos.io/tutorials/agent/connect-any-camera/).

## Part 2: Configure a Workflow and Run the Stage

```text
Agent recording -> Hub workflow -> assigned test RabbitMQ queue
                                         |
                                    Go stage
                                         |
                        preview now / HTTP callback later
                                         |
                      POST /workflows/runs/{runId} (pending)
```

Unlike the tutorial, this exercise does not publish results to
`WORKFLOWS_QUEUE`. It uses a local preview until the result API is available,
then posts the completed run over HTTP.

### 1. Configure the workflow (UI pending)

The intended UI flow is to create a workflow, select your Agent/device as its
source, add a custom stage, and assign its operation ID and queue. This UI flow
is not yet available for this exercise. For now, ask the facilitator to provide
an already routed test queue and its matching operation ID (default sample:
`forwarder`). Do not invent another queue name: the worker reads exactly the queue you configure.

The facilitator must provision the queue, routing and a user with read access
to that queue in the assigned virtual host. The worker does not declare queues,
exchanges or bindings and needs no broker management or publish permissions.
Use a dedicated queue per participant; consumers on one queue compete for
messages, they do not each receive a copy.

### 2. Open the dev container

Open the `exercise` repository itself as the VS Code folder, then run
**Dev Containers: Reopen in Container**. When using the monorepo, open its
`exercise` subfolder in a separate VS Code window first so VS Code selects
[.devcontainer/devcontainer.json](.devcontainer/devcontainer.json).

The container installs Go dependencies and runs the tests. It does not start
an Agent, a broker, or a background consumer. Start the Agent from Part 1 on
your Docker host, not inside this container.

Without a dev container, install Go 1.25.8+ and run all following commands from
this repository root with `GOWORK=off` if you are using the monorepo.

### 3. Try the stage offline

In **Run and Debug**, select **Stage: offline sample** and press **F5** (or
**Ctrl+F5** to run without debugging). No credentials, broker, or callback
server are needed. If the Go extension asks to install Delve, accept the
debugger installation.

The profiles are defined in [.vscode/launch.json](.vscode/launch.json). Open
this repository as the VS Code folder so `${workspaceFolder}` points here.
For the equivalent terminal run and the tests:

```bash
MARKER_START_TIMESTAMP=1700000000 go run . -input examples/workflow-invocation.json
go test ./...
```

The offline profile or first command prints the run ID, operation and marker envelope.
`-input` always previews locally, even when HTTP mode is configured.

Like the tutorial's starter code, this stage produces a **synthetic marker**.
It does not download video, run a model, or actually detect loitering. The
sample interval is 30 seconds; the platform derives marker duration.
The code follows the tutorial's main entry point and loitering stage:

- [main.go](main.go): connect to RabbitMQ, consume an invocation, call
  `handleInvocation`,
  and acknowledge only after delivery of the result.
- [loiter.go](loiter.go): `measureLoitering` builds a `models.Marker`, then
  `handleInvocation` wraps it in the shared `models.WorkflowResult` contract.
- [support.go](support.go): exercise configuration, preview output and the
  bearer-authenticated HTTP callback, kept separate from the tutorial flow.

The marker and block envelope use the same shared packages as the tutorial.
The worker accepts only the current `uug.ai/workflow-invocation/v1` contract,
which identifies the stage through `stage.operation` and provides a top-level
`callback` object containing the URL, HTTP method and expected result schema.
Its result is rebuilt from the operation and generated marker so
credential-bearing invocation fields are not echoed. The RabbitMQ client uses
explicit acknowledgements for preview/requeue and HTTP completion instead of
the tutorial's queue-to-queue forwarding. These are intentional exercise
adaptations.

### 4. Enter your test queue credentials

Copy [.env.example](.env.example) to `.env`, then enter the broker host,
username, password, queue name and recording timestamp supplied by the
facilitator:

```bash
cp .env.example .env
```

All Run and Debug profiles load `.env`. The file is ignored by Git; never
commit it or share it in screenshots. The profiles override only
`RESULT_MODE`, selecting `preview`, `bypass` or `http` as appropriate.

| Variable | Value |
| --- | --- |
| `RABBITMQ_HOST` | Facilitator's broker hostname and port |
| `RABBITMQ_SCHEME` | `amqps` for TLS, or `amqp` only for an approved trusted test network |
| `RABBITMQ_USERNAME`, `RABBITMQ_PASSWORD` | Your broker credentials |
| `RABBITMQ_VHOST` | Assigned virtual host, often `/` |
| `STAGE_QUEUE` | Exact existing queue that receives your dispatches |
| `STAGE_OPERATION` | Must match the invocation's `stage.operation`, default `forwarder` |
| `MARKER_START_TIMESTAMP` | Start of the test recording, in Unix seconds |
| `MARKER_DURATION_SECONDS` | Interval within that recording, default `30`, maximum `3600` |
| `RESULT_MODE` | `preview` requeues one message; `bypass` acknowledges without a callback; `http` posts the result |
| `LOG_LEVEL` | Logrus level, default `info` |
| `LOG_OUTPUT` | Logrus output format: `text` (default) or `json` |

Use the actual recording timestamp, not the current time or the fixture's
timestamp, for live dispatches. Keep the same interval on retries so marker
identity stays stable. This starter uses one configured interval: test one
recording at a time; replace `measureLoitering` with media-derived timing before
processing recordings continuously.

Broker credentials are separate from Agent credentials and the future API
bearer token. Keep them out of screenshots and commits. Do not disable TLS
certificate verification; ask the facilitator for a trusted certificate if
the broker uses a private CA. A broker on your host is not `localhost` inside
the dev container; use a hostname reachable from the container.

### 5. Consume a real dispatch now

Press **F5** with **Stage: queue preview** selected. For terminal use, load
the same file into the shell before running the stage:

```bash
set -a
. ./.env
set +a
go run .
```

Trigger a recording, or have the facilitator dispatch/re-analyse your test
recording. The worker waits for a message, builds the marker, prints a limited
preview without the raw dispatch or storage credentials, **requeues the
message and exits**. Nothing is posted to Hub and no marker is persisted yet.
Every received invocation is also logged as structured Logrus context. Secret,
token, password, credential, private-key and signed-URL fields are redacted;
credentials and query parameters embedded in URLs are removed.
Repeating the command may show the same message. Preview is not a broker peek:
it changes redelivery state and can count toward a configured delivery limit;
use the offline fixture for repeated experimentation.

To drain valid messages without calling the invocation's `callback.url`, select
**Stage: queue bypass** or set `RESULT_MODE=bypass`. Bypass prints the same
credential-free result, acknowledges the message, and continues consuming
until stopped. This permanently removes successfully processed messages from
the queue without resolving their workflow stage in Hub. Use `-once` to stop
after one acknowledged message.

Malformed messages and processing failures also requeue and stop the worker,
avoiding an immediate retry loop. Ask the facilitator to fix/quarantine an
invalid message rather than repeatedly restarting against it. Ctrl+C or a
disconnection leaves unacknowledged deliveries for RabbitMQ to recover. A
connection failure stops the process; launch it again after fixing connectivity.

### 6. Post results to Hub (API pending)

This repo includes the client, **not the Hub endpoint or token issuer**.
The proposed contract is:

```http
POST /workflows/runs/{runId}
Authorization: Bearer <workflow-scoped-token>
Content-Type: application/json
Idempotency-Key: <runId>:<operation>
```

The body is a credential-free result derived from the invocation:

```json
{
  "schema": "uug.ai/workflow-result/v1",
  "stage": {
    "operation": "forwarder"
  },
  "result": {
    "blocks": [{
      "type": "marker",
      "data": {
        "name": "forwarder-example",
        "description": "Synthetic tutorial marker; not a real loitering detection",
        "startTimestamp": 1700000000,
        "endTimestamp": 1700000030
      }
    }]
  }
}
```

The invocation itself is not echoed, so credentials and unrelated input fields
cannot leak into the result.

After the facilitator confirms the endpoint, request body, success semantics
and token issuance are implemented, select **Stage: HTTP callback (API pending)**
and press **F5**. The worker follows the shared top-level `callback` object in
each invocation. It requires `method: "POST"` and
`resultSchema: "uug.ai/workflow-result/v1"`. Before launching, set
`WORKFLOW_BEARER_TOKEN` in your ignored `.env` file. This profile sets
`RESULT_MODE=http`; credentials remain outside the committed launch
configuration. Pass `-once` explicitly for a one-delivery terminal run.

If an invocation omits `callback` or has an empty `callback.url`, HTTP mode
prints the generated result, acknowledges that delivery and continues
consuming. This fallback prevents one incomplete invocation from blocking the
queue, but it does not resolve that workflow stage in Hub. A non-empty callback
with an invalid method, schema or URL remains a processing error and is
requeued.

The callback URL may include an API prefix, for example
`https://api.example.com/api/workflows/runs/665f1b2c3d4e5f6071829304`. Its final
path segment must match the invocation's run ID. The intended spelling is
`/workflows`, not `/worfklows`. This is a workflow-completion API, not the
standalone `/ingest` API.

HTTP mode acknowledges RabbitMQ only after a `2xx` response. A timeout,
redirect, `401`, `404`, or other non-success requeues the message and exits
nonzero. Redirects are not followed; HTTPS is required except for loopback
test servers. Do not use HTTP mode against an endpoint that merely accepts
and discards results. Without `-once`, successful HTTP mode continues consuming.
Submission logs include the validated destination as the structured
`callback_url` field, together with the run ID, operation and response status.

Once available, verify that the workflow stage resolves and the marker appears
at the expected time on the recording in Hub. A successful preview alone does
not demonstrate this.

### Platform Work Still Required

- Workflow UI: select device, configure operation/queue, and activate routing.
- Result API: implement `POST /workflows/runs/{runId}` and confirm the body contract.
- Token issuance: scoped bearer tokens with expiry/revocation and permissions
  limited to the intended organisation, project, workflow and stage.
- Server authorization: look up the persisted run, validate access and expected
  operation, and derive ownership from that run rather than trusting the body.
- Completion: ingest the blocks and resolve the stage durably before success;
  deduplicate repeated `runId`/operation callbacks. A lost HTTP response can
  cause replay even when the first request succeeded.

These are follow-up tasks in the Hub repositories, not implemented here.

## Completion Checklist

- [ ] Agent has a unique device key and a working camera preview.
- [ ] Agent is online and a recording is playable in Hub.
- [ ] Dev container opens and `go test ./...` passes.
- [ ] Offline sample produces the expected marker interval.
- [ ] Assigned test queue produces a preview without leaking credentials.
- [ ] Pending UI: workflow is configured through Hub.
- [ ] Pending API: bearer-authenticated callback resolves the stage.
- [ ] Pending end-to-end check: marker is visible and retries do not duplicate it.

Submit a short note describing what you ran and which pending steps remain,
with redacted logs/screenshots. As an extension, replace the synthetic marker
with real classification trajectory analysis or downloaded-media processing;
the [finished loitering service](https://github.com/uug-ai/hub-loitering) and
[marker contract](https://learn.uug.ai/docs/hub/workflows/ingest/blocks/marker/)
provide the next steps.
