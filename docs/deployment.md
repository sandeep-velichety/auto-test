# Deployment

Testplane is one binary plus one folder (the workspace). There is no database and no other service to run. This page covers how to build it, where to run it, how to wire it into pipelines and how to look after it.

Contents: [What gets deployed](#what-gets-deployed) · [Build](#build) · [Configuration](#configuration) · [Run on a server](#run-on-a-server-systemd) · [Docker](#docker) · [Kubernetes](#kubernetes) · [Access control](#access-control) · [Workspace in Git](#keeping-the-workspace-in-git) · [CI/CD](#running-in-cicd) · [Secrets and certificates](#secrets-and-certificates) · [Network access](#network-access) · [Operations](#operations) · [Local stack](#local-stack-for-trying-kafka-mongo-and-postgres) · [What has been verified](#what-has-been-verified)

## What gets deployed

| Piece | What it is |
| --- | --- |
| `testplane` binary | Runner, REST API and the web UI (embedded). Statically linked when built with `CGO_ENABLED=0`. |
| Workspace folder | `applications/<id>/…` YAML, auth profiles, certificates. `.runs/` inside it holds run history as JSON. |

Two ways to use it, which can be combined:

- **Server mode** (`testplane serve`): testers use the UI to author and run scenarios. Needs a persistent workspace folder.
- **Headless mode** (`testplane run`): a pipeline runs scenarios from a workspace checked out of Git and publishes a JUnit report. Needs nothing persistent.

A common setup is both: testers author in the UI, the workspace folder is a Git repository, and pipelines run the committed scenarios.

## Build

Requirements: Go 1.24 or newer. Node.js 20 or newer (with npm) only if you rebuild the UI, because the built UI (`web/dist`) is committed. `make check` verifies them and reports anything missing or too old; the full list, including optional tools, is under Prerequisites in the README.

```bash
make check                                   # verify Go, Node.js and npm
make build                                   # UI + binary -> bin/testplane
make build-go                                # binary only, with the committed UI (needs only Go)
# or by hand:
CGO_ENABLED=0 go build -o bin/testplane ./cmd/testplane
```

Cross-compile for another platform:

```bash
GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 go build -o dist/testplane-linux-amd64   ./cmd/testplane
GOOS=linux   GOARCH=arm64 CGO_ENABLED=0 go build -o dist/testplane-linux-arm64   ./cmd/testplane
GOOS=darwin  GOARCH=arm64 CGO_ENABLED=0 go build -o dist/testplane-darwin-arm64  ./cmd/testplane
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o dist/testplane-windows.exe   ./cmd/testplane
```

`go.mod` contains `replace` directives that point some modules at their GitHub sources. They work on any network. To remove them, delete the replace lines and run `go mod tidy`.

## Configuration

Everything is set with command-line flags; there is no config file.

| Command | Flag | Default | Meaning |
| --- | --- | --- | --- |
| all | `--workspace DIR` | `workspace`, or `$TESTPLANE_WORKSPACE` | workspace folder |
| `serve` | `--addr HOST:PORT` | `127.0.0.1:8080` | listen address |
| `serve` | `--token T` | `$TESTPLANE_TOKEN` | access token every API call must present; empty means no check |
| `serve` | `--allow-origin URL` | any origin when a token is set, otherwise none | other web origins whose UI may connect to this backend; repeatable, `*` for any |
| `serve` | `--retention-days N` | `0` (keep all) | delete stored runs older than N days |
| `run` | `--app`, `--env`, `--scenario`, `--tag`, `--kind`, `--var`, `--junit`, `--json`, `--save`, `--publish`, `--by`, `--quiet` | | see the README |
| `validate` | `--app ID` | all applications | check YAML, auth references and certificate files |
| `demo` | `--http`, `--grpc`, `--mtls`, `--grpc-mtls`, `--workspace` | `127.0.0.1:8089/8090/8091/8092` | sample system, for trying the tool only |

Environment variables the process reads:

| Variable | Used for |
| --- | --- |
| `TESTPLANE_WORKSPACE` | default for `--workspace` |
| `TESTPLANE_TOKEN` | the backend's access token for `serve`; the token to present for `run --publish` |
| anything referenced as `{{$env.NAME}}` | secrets in auth profiles and tests |
| `HTTPS_PROXY`, `HTTP_PROXY`, `NO_PROXY` | honoured by HTTP steps and OAuth2 token requests |

Exit codes of `run`: `0` all passed, `1` at least one test failed, `2` could not run. `validate` exits `1` when it finds problems.

## Run on a server (systemd)

```bash
sudo useradd --system --home /var/lib/testplane --create-home testplane
sudo install -m 0755 bin/testplane /usr/local/bin/testplane
sudo -u testplane mkdir -p /var/lib/testplane/workspace
```

`/etc/systemd/system/testplane.service`:

```ini
[Unit]
Description=Testplane
After=network-online.target
Wants=network-online.target

[Service]
User=testplane
Group=testplane
ExecStart=/usr/local/bin/testplane serve --workspace /var/lib/testplane/workspace --addr 127.0.0.1:8080
# TESTPLANE_TOKEN (the access token) and secrets referenced as {{$env.NAME}}:
EnvironmentFile=-/etc/testplane/secrets.env
Restart=on-failure
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/var/lib/testplane

[Install]
WantedBy=multi-user.target
```

```bash
sudo install -d -m 0750 -o root -g testplane /etc/testplane
sudo install -m 0640 -o root -g testplane /dev/null /etc/testplane/secrets.env   # then add NAME=value lines
sudo systemctl daemon-reload && sudo systemctl enable --now testplane
```

Keep the listen address on `127.0.0.1` and put a reverse proxy in front of it; see [Access control](#access-control).

## Docker

The repository has a multi-stage `Dockerfile` (UI build, Go build, small Alpine runtime image, non-root user).

```bash
docker build -t testplane:0.1.0 .

docker run -d --name testplane \
  -p 127.0.0.1:8080:8080 \
  -v testplane-workspace:/workspace \
  --env-file ./secrets.env \
  testplane:0.1.0
```

The image's default command is `serve --workspace /workspace --addr 0.0.0.0:8080`. Inside a container it has to listen on all interfaces; restrict who can reach it with the `-p 127.0.0.1:…` binding or a proxy.

If you mount a host folder instead of a named volume, it must be writable by the container user:

```bash
docker run --rm testplane:0.1.0 --help >/dev/null   # image works
docker run --rm --entrypoint id testplane:0.1.0     # prints the uid to chown the folder to
```

Headless in a container, for pipelines that prefer an image to a binary:

```bash
docker run --rm -v "$PWD/workspace:/workspace:ro" -v "$PWD/reports:/reports" \
  -e ORDERS_CLIENT_SECRET \
  testplane:0.1.0 run --workspace /workspace --app orders --env staging --junit /reports/junit.xml
```

Compose, for a shared team instance:

```yaml
services:
  testplane:
    image: testplane:0.1.0
    restart: unless-stopped
    ports: ["127.0.0.1:8080:8080"]
    volumes: ["workspace:/workspace"]
    env_file: [./secrets.env]
volumes:
  workspace:
```

## Kubernetes

Run a single replica. The workspace is a folder on disk with no locking between processes, so two pods writing the same volume would overwrite each other's edits.

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata: {name: testplane-workspace}
spec:
  accessModes: [ReadWriteOnce]
  resources: {requests: {storage: 1Gi}}
---
apiVersion: v1
kind: Secret
metadata: {name: testplane-secrets}
stringData:
  TESTPLANE_TOKEN: change-me             # access token testers enter in the UI
  ORDERS_CLIENT_SECRET: change-me        # referenced as {{$env.ORDERS_CLIENT_SECRET}}
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: testplane}
spec:
  replicas: 1
  strategy: {type: Recreate}             # never two pods on the volume
  selector: {matchLabels: {app: testplane}}
  template:
    metadata: {labels: {app: testplane}}
    spec:
      securityContext: {runAsNonRoot: true, runAsUser: 1000, fsGroup: 1000}
      containers:
        - name: testplane
          image: registry.example.com/testplane:0.1.0
          args: ["serve", "--workspace", "/workspace", "--addr", "0.0.0.0:8080"]
          ports: [{containerPort: 8080}]
          envFrom: [{secretRef: {name: testplane-secrets}}]
          readinessProbe: {httpGet: {path: /api/meta, port: 8080}}
          livenessProbe: {httpGet: {path: /api/meta, port: 8080}}
          resources:
            requests: {cpu: 50m, memory: 64Mi}
            limits: {memory: 512Mi}
          volumeMounts: [{name: workspace, mountPath: /workspace}]
      volumes:
        - name: workspace
          persistentVolumeClaim: {claimName: testplane-workspace}
---
apiVersion: v1
kind: Service
metadata: {name: testplane}
spec:
  selector: {app: testplane}
  ports: [{port: 80, targetPort: 8080}]
```

Check the uid with `docker run --rm --entrypoint id <image>` and adjust `runAsUser` and `fsGroup` if it is not 1000. Expose the Service only through an Ingress that authenticates users.

`GET /api/meta` is the health endpoint: it returns 200 with a small JSON document whenever the server is up.

## Access control

Start every shared backend with an access token:

```bash
TESTPLANE_TOKEN="$(openssl rand -hex 32)" testplane serve --workspace /srv/testplane/qa --addr 0.0.0.0:8080
```

What the token does and does not do:

- Every API call must present it, so nobody can list applications, change tests, upload files or start runs without it. `GET /api/meta` stays open as the health check.
- It is one shared secret per backend, not per-user accounts. Anyone holding it can do everything. Rotate it by restarting with a new value. For named users and sign-on, put your SSO proxy (oauth2-proxy, Pomerium, an identity-aware ingress) in front as well.
- It travels in a request header, so serve the backend over TLS (terminate it at a proxy or ingress). Over plain HTTP the token can be read on the network.
- **Without a token the backend is open** to anyone who can reach the port. That is acceptable on `127.0.0.1` (the default address) and nowhere else.

Even with the token, stored secrets are write-only through the API: auth profile secrets come back masked, key files are never returned and zip exports contain neither.

**Connecting a UI to several backends.** Any backend's UI can connect to any other backend: testers add the address and token under "Backends". For the browser to allow it, the target backend must permit the calling page's origin. With a token set, every origin is permitted by default (the token is what protects it). To restrict that, list the allowed UI origins:

```bash
testplane serve … --token "$TOKEN" --allow-origin https://tests.example.com
```

A backend without a token only accepts its own UI unless `--allow-origin` is given.

Minimal nginx example with TLS and basic auth:

```nginx
server {
  listen 443 ssl;
  server_name testplane.internal.example.com;
  ssl_certificate     /etc/ssl/testplane.crt;
  ssl_certificate_key /etc/ssl/testplane.key;

  auth_basic           "Testplane";
  auth_basic_user_file /etc/nginx/testplane.htpasswd;

  client_max_body_size 50m;            # zip imports
  location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_read_timeout 600s;           # a run answers when it finishes
  }
}
```

The run endpoint is synchronous: the HTTP request stays open until the run ends. Set proxy and ingress read timeouts above your longest run.

## Keeping the workspace in Git

The workspace is plain files, so Git gives you review, history and distribution for free.

```bash
cd /var/lib/testplane/workspace
git init && git remote add origin git@github.com:your-org/api-tests.git
```

Suggested `.gitignore` for a workspace repository:

```gitignore
.runs/
*.tmp
# never commit private keys
applications/*/assets/certs/*.key
```

Flow that works well: testers edit in the UI on a shared instance, someone commits and opens a pull request, pipelines run what is on the main branch. Testplane does not commit or pull for you.

To hand an application to another team without Git, use **Download as zip** in the UI (or `GET /api/apps/{id}/export`) and **Import zip** on the other side. Private keys are left out of the zip and stored secrets are replaced with `CHANGE_ME`; the receiving team supplies its own.

## Running in CI/CD

The pattern is the same everywhere: get the binary, check out the workspace, supply secrets as environment variables, run, publish the JUnit file.

```bash
testplane validate --workspace . --app orders
testplane run --workspace . --app orders --env staging --tag smoke --junit reports/junit.xml
```

Useful selections: `--tag smoke` for a quick gate, `--kind positive` to skip negative cases, `--scenario a,b` for specific scenarios, `--var baseUrl=https://pr-123.example.com` to point at an ephemeral environment.

Getting the binary into a job: build it in the job (`go build`, about a minute), download it from wherever you publish releases, or use the Docker image.

### GitHub Actions

```yaml
name: api-tests
on:
  workflow_dispatch:
  schedule: [{cron: "0 6 * * *"}]
jobs:
  api-tests:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4                       # the workspace repository
      - uses: actions/checkout@v4
        with: {repository: sandeep-velichety/auto-test, path: tool}
      - uses: actions/setup-go@v5
        with: {go-version: "1.24"}
      - run: cd tool && CGO_ENABLED=0 go build -o "$RUNNER_TEMP/testplane" ./cmd/testplane
      - name: Client key for mTLS
        run: |
          printf '%s' "$ORDERS_CLIENT_KEY" > applications/orders/assets/certs/client.key
          chmod 600 applications/orders/assets/certs/client.key
        env:
          ORDERS_CLIENT_KEY: ${{ secrets.ORDERS_CLIENT_KEY }}
      - run: |
          "$RUNNER_TEMP/testplane" validate --workspace . --app orders
          "$RUNNER_TEMP/testplane" run --workspace . --app orders --env staging --junit reports/junit.xml
        env:
          ORDERS_CLIENT_SECRET: ${{ secrets.ORDERS_CLIENT_SECRET }}
      - uses: actions/upload-artifact@v4
        if: always()
        with: {name: junit, path: reports/junit.xml}
```

### GitLab CI

```yaml
api-tests:
  image: golang:1.24
  variables:
    GIT_DEPTH: "1"
  script:
    - git clone --depth 1 https://github.com/sandeep-velichety/auto-test /tmp/tool
    - (cd /tmp/tool && CGO_ENABLED=0 go build -o /usr/local/bin/testplane ./cmd/testplane)
    - testplane validate --workspace . --app orders
    - testplane run --workspace . --app orders --env staging --junit reports/junit.xml
  artifacts:
    when: always
    reports:
      junit: reports/junit.xml
```

Define `ORDERS_CLIENT_SECRET` as a masked CI/CD variable.

### Jenkins

```groovy
pipeline {
  agent any
  environment { ORDERS_CLIENT_SECRET = credentials('orders-client-secret') }
  stages {
    stage('API tests') {
      steps {
        sh 'testplane validate --workspace . --app orders'
        sh 'testplane run --workspace . --app orders --env staging --junit reports/junit.xml'
      }
    }
  }
  post { always { junit 'reports/junit.xml' } }
}
```

Assumes `testplane` is installed on the agent.

### Azure Pipelines

```yaml
steps:
  - script: |
      testplane validate --workspace . --app orders
      testplane run --workspace . --app orders --env staging --junit reports/junit.xml
    env:
      ORDERS_CLIENT_SECRET: $(ordersClientSecret)
  - task: PublishTestResults@2
    condition: always()
    inputs: {testResultsFormat: JUnit, testResultsFiles: reports/junit.xml}
```

In the JUnit file each scenario is a test suite and each test or case is a test case named `[kind] name`, with the test's properties attached.

### Publishing pipeline runs to a backend

A pipeline can keep its history on the shared backend, next to the runs testers start from the UI:

```bash
TESTPLANE_TOKEN="$BACKEND_TOKEN" testplane run --workspace . --app orders --env staging \
  --junit reports/junit.xml --publish https://tests.example.com --by "pipeline #$BUILD_NUMBER"
```

The application must exist on that backend. The tests still run on the build agent; only the result is sent.

## Secrets and certificates

- **Secrets:** write `{{$env.NAME}}` in an auth profile instead of the value, and provide `NAME` to the process (systemd `EnvironmentFile`, container env, Kubernetes Secret, CI secret). The value is resolved at run time and is not written back to disk. A missing variable fails the test with "undefined variable".
- **Secrets typed straight into a profile** are stored as plain text in `assets/auth/<id>.yaml` (file mode `0600`). Fine for throwaway test credentials; avoid it for anything that matters.
- **Certificates:** PEM files in `applications/<id>/assets/certs`, named by certificate sets in `assets/tls` (which can point at different files per environment). Private keys must be unencrypted PEM. The API lists what a file is (certificate, key, subject, expiry) and never returns its contents. Uploaded files are written with mode `0600`.
- **Keys in pipelines:** keep the key out of Git and write it into `assets/certs/` from a CI secret before the run, as in the GitHub Actions example.
- **Expiry:** `testplane validate` fails when a referenced certificate is missing, expired, of the wrong kind or does not match its key. Running it on a schedule gives early warning.
- **Run history** (`.runs/`) stores requests and responses of each test, truncated at 64 KB per body. Tokens added by auth profiles are not recorded and Datadog keys are masked; anything else you put in a step's own headers or body is stored.
- **Datadog keys** for `logs` steps: provide `DD_API_KEY` and `DD_APP_KEY` to the backend process and reference them as `{{$env.DD_API_KEY}}` and `{{$env.DD_APP_KEY}}`. The application key needs permission to read logs.
- **Browser storage:** the UI remembers each backend's address and token in the browser's local storage on the tester's machine.

## Network access

The process running the tests needs to reach what the tests call: the HTTP and gRPC endpoints, OAuth2 token endpoints, Kafka brokers (every broker, not only the bootstrap address, on the addresses the brokers advertise), MongoDB and PostgreSQL. In server mode that is the server; in a pipeline it is the build agent. Inbound, only the UI/API port is needed, and only in server mode.

## Operations

**Backup.** Copy the workspace folder. If it is in Git, `applications/` is already safe and only `.runs/` and private keys need a separate copy.

**Run history.** Every run from the UI, every published pipeline run and every CLI run with `--save` adds one JSON file under `.runs/<app>/`, named by start time. Start the backend with `--retention-days N` to delete older runs automatically (checked at start-up and after each stored run). Without it nothing is pruned; a cron entry also works:

```bash
find /var/lib/testplane/workspace/.runs -name '*.json' -mtime +30 -delete
```

**Upgrade.** Replace the binary (or image tag) and restart. There is no migration step; the workspace format is the YAML itself. Run `testplane validate --workspace …` after an upgrade to confirm every file still parses.

**Logs.** The server prints start-up information to standard output; follow it with `journalctl -u testplane` or `docker logs`. There is no access log, so take that from the reverse proxy.

**Concurrency.** Runs execute their scenarios one after another. Several people can start runs at the same time; each run is independent. Two people saving the same scenario at the same moment is last-write-wins.

**Troubleshooting**

| Symptom | Likely cause |
| --- | --- |
| `undefined variable(s): {{x}}` | the selected environment lacks `x`, or an earlier step that captures it did not pass |
| `auth profile "…": token endpoint … answered 401` | wrong client id or secret, or `{{$env.…}}` resolved to the wrong value |
| `x509: certificate signed by unknown authority` | the service uses a private CA: add it as the profile's CA bundle |
| `remote error: tls: … certificate required` | the service wants a client certificate: use an mTLS profile |
| `this backend needs its access token` | the backend was started with `--token`: enter it under Backends, or set `TESTPLANE_TOKEN` for `run --publish` |
| "Cannot reach …" when adding a backend | wrong address, backend down, or it does not allow this page's origin (start it with a token or `--allow-origin`) |
| UI shows "UI not built" | the binary was built without `web/dist`: run `make ui` and rebuild |
| Kafka step connects, then times out | brokers advertise an address the runner cannot reach |
| `permission denied` writing the workspace in Docker | the mounted folder is not writable by the container user |

## Local stack for trying Kafka, Mongo and Postgres

`examples/docker-compose.yml` starts the three servers, creates the `orders.events` topic and seeds one Mongo document. The `data-demo` example application is preconfigured for it.

```bash
docker compose -f examples/docker-compose.yml up -d
./bin/testplane run --workspace examples/workspace --app data-demo
docker compose -f examples/docker-compose.yml down -v
```

## What has been verified

So you know what to trust on first use:

- Verified by automated tests or by running it: the binary, the CLI, the REST API, the UI, access tokens and cross-backend connections, run history and publishing, HTTP, gRPC, OAuth2, mTLS and Postgres steps, cross-application flows, and the Postgres steps of the `data-demo` scenario.
- Written but not yet run, because the build environment had no Docker daemon, Kafka broker or MongoDB server: the `Dockerfile`, `examples/docker-compose.yml`, the Kubernetes manifest and the CI snippets on this page, and the Kafka and Mongo steps. The Datadog `logs` step has only been run against a stand-in server that mimics the Logs Search API. Expect to adjust small details the first time you use them.
