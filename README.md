# Kerberos Platform Introduction

This exercise introduces the Kerberos platform through one complete media flow:

```text
Camera -> Agent -> Hub -> Workflow -> Your stage -> Marker -> Hub timeline
```

By the end, you will have connected a camera to Hub and built a small service
that receives a recording, processes it, and sends a marker back to the
platform.

Allow about 60-90 minutes. The custom-stage part requires a self-hosted Hub
deployment. If you only have access to the managed Hub, ask your facilitator
for a development environment or use the HTTP ingest API to practise the marker
contract.

## Prerequisites

- A Hub account and its API URI, public access key, and private key
- An H.264 or H.265 RTSP camera stream
- Docker
- For the workflow stage: a self-hosted Hub with workflows, RabbitMQ, and
  MongoDB enabled
- `kubectl` and `helm` access to that Hub cluster
- A container registry to which you can push an image
- A programming language with a RabbitMQ client; the references use Go

Do not commit camera passwords, Hub credentials, broker credentials, or storage
credentials to this repository.

## Part 1: Connect an Agent to Hub

An Agent runs close to one camera. It reads the camera stream, creates
recordings, and sends them to Hub for central processing and review.

### 1. Get the Hub credentials

Sign in to Hub, open **Devices**, and select **Add Agent**. Copy the values shown
for:

- Hub API URI
- Public access key
- Private key
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

## Part 2: Build a Workflow Stage

A workflow is a branch from Hub's standard media pipeline. The workflows engine
dispatches a `WorkflowRun` to a stage-specific queue. Your service consumes the
run, processes the referenced recording, and publishes the completed run back
to the workflows queue.

Your stage should perform a small, observable operation on the media. Keep the
first version simple, for example:

- Extract one frame and classify it as bright or dark.
- Read the clip duration and mark its midpoint.
- Detect a dominant colour in one frame.
- Run a small model and mark the interval containing a result.

The goal is to learn the integration path, not to build a production model.

### 1. Define the stage

Choose these values and record them in your pull request:

| Setting | Example |
| --- | --- |
| Operation ID | `introduction-marker` |
| Stage queue | `hub-workflows-introduction-marker` |
| Result block type | `marker` |
| Processing rule | `Mark the midpoint of every recording` |

Operation IDs and queue names must be unique in the shared environment.

### 2. Implement the worker

Your service must:

1. Connect to RabbitMQ using the injected `RABBITMQ_*` variables.
2. Consume a `WorkflowRun` from its stage queue.
3. Use `run.key` and the credentials in `run.storage` to fetch the recording.
4. Process the recording and derive a meaningful timestamp or interval.
5. Copy the received run, preserve its identity fields, set its operation to
   your operation ID, and clear `storage` so credentials are not echoed.
6. Put a block envelope on `payload` and publish the run to
   `WORKFLOWS_QUEUE`.
7. Handle redelivery idempotently and emit useful logs containing `runId`,
   `traceId`, and the media key.

The returned payload has this shape:

```json
{
  "blocks": [
    {
      "type": "marker",
      "data": {
        "name": "introduction-result",
        "startTimestamp": 1752482068,
        "endTimestamp": 1752482073,
        "duration": 5,
        "description": "Result produced by the introduction stage",
        "categories": [{ "name": "exercise" }],
        "tags": [{ "name": "introduction-marker" }]
      }
    }
  ]
}
```

Marker timestamps are Unix seconds, not milliseconds, frame numbers, or offsets.
The platform supplies organisation and device ownership from the persisted run;
do not accept or invent those fields in the marker. A marker is upserted by its
device, name, and start timestamp, so keep those values stable on redelivery.

Start from one of these references:

- [`hub-speed`](https://github.com/uug-ai/hub-workflows/tree/main/services/hub-speed):
  a compact worker scaffold intended to be copied and adapted
- [`hub-loitering`](https://github.com/uug-ai/hub-loitering): a complete marker
  stage
- [Custom workflow stage tutorial](https://doc.kerberos.io/tutorials/hub/custom-workflow-stage/):
  the full queue, implementation, container, and deployment walkthrough
- [Marker contract](https://doc.kerberos.io/docs/hub/workflows/ingest/blocks/marker/):
  accepted marker fields and persistence behaviour

Keep processing logic separate from queue transport so it can be unit tested
without RabbitMQ.

### 3. Test and package the stage

At minimum, add tests that prove:

- A valid run produces the expected marker interval.
- A redelivered run produces the same marker identity.
- Invalid or missing media does not produce a successful result.
- Storage credentials are absent from the returned run.

Build a container image and push a versioned tag to your registry. Do not use
`latest` for the deployed stage.

### 4. Register the workflow and service

Add the stage and its deployment to the Hub Helm values. The stage and service
names must match, and the queue must match the queue consumed by your worker:

```yaml
kerberoshub:
  workflows:
    enabled: true
    stages:
      introduction-marker:
        enabled: true
        dispatch: always

  services:
    introduction-marker:
      enabled: true
      repository: ghcr.io/your-org/hub-introduction-marker
      tag: "v0.1.0"
      queue: "hub-workflows-introduction-marker"
      replicas: 1
      pullPolicy: IfNotPresent
      logLevel: info
```

Use `dispatch: always` while proving the wiring. Once it works, add a condition
so the stage only runs for relevant media. Apply the values using your
environment's normal deployment process.

### 5. Verify the complete flow

Trigger a new Agent recording, then verify each boundary:

1. The recording appears in Hub.
2. `hub-workflows` logs a dispatch to your operation and queue.
3. Your worker logs that it fetched and processed the recording.
4. Your worker publishes a result to `WORKFLOWS_QUEUE` without storage
   credentials.
5. The workflow run completes without an unresolved stage.
6. The marker appears at the expected position on the recording timeline and
   in **Analysis > Markers** when that feature is enabled.

Useful commands:

```bash
kubectl -n kerberos-hub logs deploy/hub-workflows -f
kubectl -n kerberos-hub logs deploy/hub-introduction-marker -f
```

Part 2 is complete when a recording created by your Agent is processed by your
stage and its marker is visible in Hub.

## Completion Checklist

- [ ] Agent runs with a unique device key.
- [ ] Camera preview and recording work locally.
- [ ] Agent is online and its recording is playable in Hub.
- [ ] A versioned stage image is deployed.
- [ ] A workflow dispatches recordings to the stage.
- [ ] The stage reads actual media and returns a valid marker block.
- [ ] Returned runs do not contain storage credentials.
- [ ] Reprocessing does not create duplicate markers.
- [ ] The marker is visible on the recording timeline.

When you submit the exercise, include the stage source, tests, deployment
configuration, and a short note describing what the stage processes. Include
logs or screenshots that demonstrate the final Agent-to-marker flow, with all
credentials redacted.
