# YAML reference

## application.yaml

```yaml
id: orders
name: Orders service
defaultEnvironment: staging
defaultAuth: orders-oauth          # used by tests that do not pick a profile
environments:
  staging:
    baseUrl: https://orders.staging.example.com
    grpcTarget: orders.staging.example.com:443
    kafkaBrokers: kafka-1:9093,kafka-2:9093
    mongoUri: mongodb://mongo.staging:27017
    postgresDsn: postgres://qa:{{$env.PG_PASSWORD}}@pg.staging:5432/orders?sslmode=require
```

With regions, each environment key is an `environment/region` pair holding that pair's full set of values:

```yaml
defaultEnvironment: stage/NA
environments:
  stage/NA: {baseUrl: "https://orders.na.stage.example.com"}
  stage/AU: {baseUrl: "https://orders.au.stage.example.com"}
  prod/NA:  {baseUrl: "https://orders.na.example.com"}
  prod/AU:  {baseUrl: "https://orders.au.example.com"}
```

Run a pair with `--env stage --region AU` (or `--env stage/AU`). In tests, `{{$environment}}` and `{{$region}}` give the two names. In the `environments:` block of an auth profile or certificate set, a key may be an environment (`stage`), a region (`AU`) or a pair (`stage/AU`); all that match are applied in that order, so the most specific wins.

## Variables

`{{name}}` is replaced in every string of a test, an expectation and an auth profile. Lowest to highest precedence: environment, scenario `variables`, `--var` overrides, values captured by earlier steps. An undefined variable fails the test with a clear message.

Values of other applications are available as `{{app-id.name}}`, taken from that application's environment with the same name as the one being run, or else its default environment.

Built in: `{{$scenarioStart}}` (ISO time the scenario began), `{{$uuid}}`, `{{$timestamp}}`, `{{$timestampMs}}`, `{{$isoTimestamp}}`, `{{$randomInt}}`, `{{$env.NAME}}`. Built-ins produce a new value each time they appear; put one in a scenario variable to reuse the same value across steps.

## Certificate sets (`assets/tls/<id>.yaml`)

A certificate set names the files to present for mutual TLS. Files are PEM and live in `assets/certs`.

```yaml
id: client
name: Orders client certificate
hosts:                               # optional: present automatically on calls to these hosts
  - "{{ordersHost}}"                 # host, host:port or *.example.com; variables allowed
  - "*.internal.example.com:8443"
cert: client.crt                     # client certificate
key: client.key                      # private key, unencrypted
ca: internal-ca.pem                  # only when the service's certificate comes from a private CA
serverName: orders.internal          # optional: when the certificate name differs from the host
insecureSkipVerify: false
environments:                        # only the fields that differ
  staging:
    cert: client-staging.crt
    key: client-staging.key
```

A set with only `ca` trusts that CA and presents no client certificate.

Which set a call uses, first match wins:

1. `mtls` on the case, then on the test, then on the scenario. `mtls: none` presents nothing.
2. Otherwise the application's set whose `hosts` match the host being called (HTTP and logs: the URL's host; gRPC: the target).
3. Otherwise the TLS settings of the auth profile, if it has any (older style, below).

`mtls: other-app/client` borrows a set from another application.

## Auth profiles (`assets/auth/<id>.yaml`)

Token from an identity provider (OAuth2). The token is fetched once per run:

```yaml
id: orders-oauth
name: Orders OAuth2 client
type: oauth2_client_credentials      # or oauth2_password
oauth2:
  tokenUrl: "{{baseUrl}}/oauth/token"
  clientId: qa-client
  clientSecret: "{{$env.ORDERS_CLIENT_SECRET}}"
  scopes: [orders.read, orders.write]
  audience: orders-api               # optional
  clientAuth: basic                  # optional: send client id/secret as HTTP Basic instead of in the body
  tokenField: access_token           # optional: path of the token in the response
  headerName: Authorization          # optional
  prefix: Bearer                     # optional; "none" sends the bare token
  username: svc-user                 # oauth2_password only
  password: "{{$env.SVC_PASSWORD}}"  # oauth2_password only
headers:                             # optional on every type: extra headers sent with each request
  X-Tenant: acme
environments:                        # optional on every type: per-environment differences, deep-merged
  staging:
    oauth2:
      clientId: qa-client-staging
      clientSecret: "{{$env.ORDERS_STAGING_SECRET}}"
```

Token URL patterns for two common providers (replace the placeholders):

| Provider | `tokenUrl` | Notes |
| --- | --- | --- |
| Okta | `https://<your-domain>/oauth2/<authorization-server-id>/v1/token` | `default` is the built-in authorization server; needs at least one scope; `clientAuth: basic` is the usual choice |
| ForgeRock AM | `https://<host>/am/oauth2/realms/root/realms/<realm>/access_token` | some installations use `/openam` instead of `/am` |

Token from a custom HTTP call, for logins that are not OAuth2:

```yaml
id: login-call
name: Token from a custom login call
type: http_call
httpCall:
  method: POST                       # default
  url: "{{baseUrl}}/auth/login"
  headers: {X-Client-Id: qa}
  query: {realm: alpha}
  body: {username: qa-user, password: "{{$env.QA_PASSWORD}}"}   # a map is sent as JSON
  # form: {username: qa-user}        # alternative: urlencoded form
  tokenPath: data.session.token      # where the token is; $header.Name reads a response header
  headerName: Authorization          # header that carries the token on each test request
  prefix: Bearer                     # "none" sends the bare token
```

Other types:

```yaml
type: headers                        # fixed headers / keys
headers: {X-Api-Key: "{{$env.API_KEY}}", X-Tenant: acme}
---
type: api_key
apiKey: {name: X-Api-Key, value: "{{$env.API_KEY}}", in: header}   # or query
---
type: bearer
bearer: {token: "{{$env.API_TOKEN}}"}
---
type: basic
basic: {username: qa, password: "{{$env.QA_PASSWORD}}"}
```

Older style, still supported: a profile can carry its own `tls: {cert, key, ca}` block, and `type: mtls` is a profile that is nothing but that block. Prefer certificate sets; a set chosen on the test, or matched by host, takes the place of the profile's block.

Through the API and UI, secret values (passwords, tokens, client secrets, API key values, every header and form value) are write-only: they come back as `********`. `{{$env.NAME}}` references are shown as written.

How credentials reach each step type:

| Step | Headers / token | Username and password (basic) | TLS files |
| --- | --- | --- | --- |
| http | request headers or query | Basic header | client certificate, CA |
| grpc | call metadata | Basic metadata | client certificate, CA |
| kafka | not used | SASL when `sasl` is set | client certificate, CA |
| mongo | not used | connection credentials | client certificate, CA |
| postgres | not used | not used (put them in the DSN) | `sslcert`, `sslkey`, `sslrootcert` |

## Scenario (`scenarios/<id>.yaml`)

```yaml
id: order-lifecycle
name: Order lifecycle
description: optional
tags: [smoke]
properties: {component: orders}
auth: orders-oauth             # default for every step, optional
mtls: client                   # default certificate set for every step, optional
variables: {item: "widget-{{$randomInt}}"}
tests: [...]
```

### Fields shared by every test

| Field | Meaning |
| --- | --- |
| `id`, `name` | required; ids are lowercase letters, digits, `-`, `_` |
| `type` | `http`, `grpc`, `kafka`, `mongo`, `postgres`, `logs` |
| `kind` | `positive` (default) or `negative` |
| `auth` | profile id, `other-app/profile-id` to borrow another application's profile, or `none`; default inherits |
| `mtls` | certificate set id, `other-app/set-id`, or `none`; default inherits, then matches by host |
| `properties` | free-form labels |
| `expect` | see below |
| `capture` | `name: path`; also `$status` and `$header.Name` |
| `variants` | paired cases, see below |
| `retry` | `{attempts: 5, delayMs: 500}` re-runs until the expectation holds |
| `skip`, `continueOnFail` | booleans |

### http

```yaml
http:
  method: POST
  url: "{{baseUrl}}/orders"
  headers: {X-Tenant: acme}     # an empty value removes a header, including one from the auth profile
  query: {expand: lines}
  body: {item: widget}          # a map is sent as JSON; a string is sent as is
  form: {a: b}                  # alternative: urlencoded form
  timeoutMs: 30000
  followRedirects: false
```

### grpc

```yaml
grpc:
  target: "{{grpcTarget}}"
  method: orders.v1.OrderService/GetOrder
  plaintext: false              # true for servers without TLS
  metadata: {x-tenant: acme}
  message: {id: "{{orderId}}"}
  protoFiles: [orders.proto]    # from assets/protos; omit to use server reflection
```

The result document is the response message as JSON. A non-OK status is a normal result: assert it with `expect.code`.

Streaming: a server-streaming method returns `{responses: [...]}` when it sends more than one message. For a client-streaming method, give `message` as a list and each element is sent in turn.

### kafka

```yaml
- id: order-created-event
  name: OrderCreated is published
  type: kafka
  kafka:
    brokers: "{{kafkaBrokers}}"
    topic: orders.events
    action: consume               # default
    match:                        # the message to wait for
      - {path: value.orderId, value: "{{orderId}}"}
      - {path: value.type, value: OrderCreated}
    from: scenario_start          # default; `earliest` reads the whole topic
    timeoutMs: 10000
    tls: true                     # or use an auth profile with tls
    sasl: scram-sha-512           # plain | scram-sha-256 | scram-sha-512, credentials from a basic profile
  expect:
    body:
      - {path: value.total, op: gt, value: 0}
```

Order of operations: before the first step of a scenario runs, the end of every topic that a consume step will read is recorded. A consume step therefore sees everything published since the scenario began, so it can simply be placed after the call that triggers the event. The result document is `{found, topic, partition, offset, key, value, headers, time}`.

A negative Kafka test (`kind: negative` or `expect: {found: false}`) passes when no matching message arrives before the timeout.

`action: produce` publishes `key`, `value` and `headers`.

### mongo

```yaml
- id: order-stored
  name: Order document exists
  type: mongo
  mongo:
    uri: "{{mongoUri}}"
    database: orders
    collection: orders
    operation: find               # find (default) | count | aggregate
    filter: {orderId: "{{orderId}}"}     # extended JSON works: {_id: {$oid: "..."}}
    sort: {createdAt: -1}
    limit: 10
  expect:
    count: 1
    body:
      - {path: documents.0.status, value: created}
```

The result document is `{count, documents: [...]}` in relaxed extended JSON, so an ObjectId reads as `documents.0._id.$oid`.

### postgres

```yaml
- id: order-row
  name: Order row exists
  type: postgres
  retry: {attempts: 5, delayMs: 300}
  postgres:
    dsn: "{{postgresDsn}}"
    query: SELECT status, total FROM orders WHERE id = $1
    args: ["{{orderId}}"]
  expect:
    body:
      - {path: rows.0.status, value: created}
```

The result document is `{count, rows: [{column: value}]}`. Any statement can be used, which is handy for set-up and clean-up steps.

### logs

```yaml
- id: order-logged
  name: The service logged the order
  type: logs
  retry: {attempts: 6, delayMs: 5000}     # logs take a few seconds to become searchable
  logs:
    provider: datadog                     # the only provider so far
    site: datadoghq.com                   # or datadoghq.eu, us5.datadoghq.com, ...
    query: "service:orders @orderId:{{orderId}}"
    from: scenario_start                  # default; or now-15m, or an ISO time
    limit: 50
    apiKey: "{{$env.DD_API_KEY}}"
    appKey: "{{$env.DD_APP_KEY}}"
  expect:
    body:
      - {path: logs.0.message, op: contains, value: created}
      - {path: logs.0.attributes.http.status_code, value: 201}
```

Uses Datadog's Logs Search API (`POST /api/v2/logs/events/search`), which needs both an API key and an application key. The result document is `{count, logs: [{id, timestamp, status, service, host, message, tags, attributes}]}`, oldest first. Keys can also come from an `api_key` auth profile whose header name is `DD-API-KEY`. Both keys are masked in stored run results.

A borrowed profile's own placeholders are filled from its owner's environment: `orders/orders-oauth` with `tokenUrl: "{{baseUrl}}/oauth/token"` uses the `orders` application's `baseUrl`.

## expect

| Field | Applies to | Meaning |
| --- | --- | --- |
| `status`, `statusIn` | http | exact status, or any of a list |
| `code` | grpc | code name such as `NOT_FOUND` |
| `count` | mongo, postgres, logs | exact number of documents, rows or log entries |
| `found` | kafka | `true` or `false` |
| `headers` | http | response header equals |
| `body` | all | list of checks on the result document |
| `bodyContains` | all | raw substrings |
| `maxDurationMs` | all | latency budget |
| `error` | all | the call must fail before any response (refused TLS handshake, connection refused, rejected token request) with a message containing this text; `"*"` accepts any such failure |

When the main check (`status`, `code`, `count`, `found`) is left out, a default applies:

| Type | positive | negative |
| --- | --- | --- |
| http | status is 2xx | status is not 2xx |
| grpc | code is OK | code is not OK |
| kafka consume | a matching message arrives | none arrives |
| mongo, postgres read, logs | at least one document, row or log entry | none |
| postgres non-SELECT | the statement runs | the statement runs |

A check is `{path, op, value}`. Paths use [gjson syntax](https://github.com/tidwall/gjson/blob/master/SYNTAX.md): `id`, `items.0.sku`, `items.#` (array length), `items.#(sku=="A1").qty`. Operators: `equals` (default), `not_equals`, `contains`, `not_contains`, `matches` (regular expression), `exists`, `not_exists`, `gt`, `gte`, `lt`, `lte`, `length`, `in`. Comparison is loose about numbers and strings, so a captured `"42"` equals `42`.

## variants (paired cases)

```yaml
variants:
  - id: wrong-tenant
    name: Other tenant cannot read the order
    kind: negative          # default for a case
    auth: other-tenant      # optional
    mtls: none              # optional
    properties: {priority: high}
    http:                   # deep-merged over the parent's http block
      headers: {X-Tenant: other}
      query: {expand: null} # null removes a field
    expect: {status: 403}
```

The override block is named after the parent's type (`http`, `grpc`, `kafka`, `mongo`, `postgres`, `logs`). Maps merge key by key; lists and scalars are replaced. Result ids are `<test>.<case>`.

## REST API

All under `/api`; bodies are JSON unless noted. When the backend was started with a token, every call except `GET /meta` needs `Authorization: Bearer <token>`. `X-Testplane-User: <name>` on a run request is recorded as who started it.

| Method and path | Purpose |
| --- | --- |
| `GET /meta` | health check; says whether a token is required |
| `GET /apps`, `GET/PUT/DELETE /apps/{app}` | applications |
| `GET /apps/{app}/scenarios`, `GET/PUT/DELETE /apps/{app}/scenarios/{id}` | scenarios |
| `GET/PUT /apps/{app}/scenarios/{id}/yaml` | the stored file, as text |
| `GET /apps/{app}/scenarios/{id}/export` | the scenario with the auth profiles and certificate sets it uses, as one YAML file (secrets replaced) |
| `POST /apps/{app}/scenarios/import?overwrite=` | body: that file, or a plain scenario YAML; creates only missing profiles and sets |
| `POST /apps/{app}/grpc/describe` | `{environment, auth, mtls, target, plaintext, protoFiles}`; lists services and methods with request templates |
| `GET /apps/{app}/tls?env=`, `PUT/DELETE /apps/{app}/tls/{id}` | certificate sets, with file checks for that environment |
| `GET /apps/{app}/auth?env=`, `PUT/DELETE /apps/{app}/auth/{id}` | auth profiles; secrets come back as `********`, and sending `********` keeps the stored value |
| `POST /apps/{app}/auth/{id}/check?env=&mtls=` | try a profile as a run in that environment would, optionally with a certificate set |
| `GET /apps/{app}/assets`, `PUT/DELETE /apps/{app}/assets/{certs\|protos}/{name}` | files (raw body on upload); contents are never returned |
| `POST /apps/{app}/run` | `{environment, scenarios, tags, kind, vars, until}`; runs, stores and returns the result. `until` stops each scenario after that test id and does not store the run |
| `GET /apps/{app}/runs?scenario=&status=&env=&from=&to=&limit=` | history, newest first, without test detail; dates are `YYYY-MM-DD` or RFC 3339; with `scenario`, status and totals describe that scenario |
| `GET /apps/{app}/runs/{id}` | one run in full |
| `POST /apps/{app}/runs` | store a result produced elsewhere (`testplane run --publish`) |
| `GET /apps/{app}/export`, `POST /import` | zip out (no keys, no literal secrets), zip in |
