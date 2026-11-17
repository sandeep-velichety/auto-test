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

## Variables

`{{name}}` is replaced in every string of a test, an expectation and an auth profile. Lowest to highest precedence: environment, scenario `variables`, `--var` overrides, values captured by earlier steps. An undefined variable fails the test with a clear message.

Built in: `{{$uuid}}`, `{{$timestamp}}`, `{{$timestampMs}}`, `{{$isoTimestamp}}`, `{{$randomInt}}`, `{{$env.NAME}}`. Built-ins produce a new value each time they appear; put one in a scenario variable to reuse the same value across steps.

## Auth profiles (`assets/auth/<id>.yaml`)

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
tls:                                 # optional on every type
  cert: client.crt                   # file names in assets/certs
  key: client.key
  ca: internal-ca.pem
  serverName: orders.internal        # optional
  insecureSkipVerify: false
```

Other types:

```yaml
type: basic
basic: {username: qa, password: "{{$env.QA_PASSWORD}}"}
---
type: bearer
bearer: {token: "{{$env.API_TOKEN}}"}
---
type: api_key
apiKey: {name: X-Api-Key, value: "{{$env.API_KEY}}", in: header}   # or query
---
type: mtls
tls: {cert: client.crt, key: client.key, ca: internal-ca.pem}
```

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
variables: {item: "widget-{{$randomInt}}"}
tests: [...]
```

### Fields shared by every test

| Field | Meaning |
| --- | --- |
| `id`, `name` | required; ids are lowercase letters, digits, `-`, `_` |
| `type` | `http`, `grpc`, `kafka`, `mongo`, `postgres` |
| `kind` | `positive` (default) or `negative` |
| `auth` | profile id, or `none`; default inherits |
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

## expect

| Field | Applies to | Meaning |
| --- | --- | --- |
| `status`, `statusIn` | http | exact status, or any of a list |
| `code` | grpc | code name such as `NOT_FOUND` |
| `count` | mongo, postgres | exact number of documents or rows |
| `found` | kafka | `true` or `false` |
| `headers` | http | response header equals |
| `body` | all | list of checks on the result document |
| `bodyContains` | all | raw substrings |
| `maxDurationMs` | all | latency budget |
| `error` | all | the call must fail before any response (refused TLS handshake, connection refused, rejected token request) with a message containing this text |

When the main check (`status`, `code`, `count`, `found`) is left out, a default applies:

| Type | positive | negative |
| --- | --- | --- |
| http | status is 2xx | status is not 2xx |
| grpc | code is OK | code is not OK |
| kafka consume | a matching message arrives | none arrives |
| mongo, postgres read | at least one document or row | none |
| postgres non-SELECT | the statement runs | the statement runs |

A check is `{path, op, value}`. Paths use [gjson syntax](https://github.com/tidwall/gjson/blob/master/SYNTAX.md): `id`, `items.0.sku`, `items.#` (array length), `items.#(sku=="A1").qty`. Operators: `equals` (default), `not_equals`, `contains`, `not_contains`, `matches` (regular expression), `exists`, `not_exists`, `gt`, `gte`, `lt`, `lte`, `length`, `in`. Comparison is loose about numbers and strings, so a captured `"42"` equals `42`.

## variants (paired cases)

```yaml
variants:
  - id: wrong-tenant
    name: Other tenant cannot read the order
    kind: negative          # default for a case
    auth: other-tenant      # optional
    properties: {priority: high}
    http:                   # deep-merged over the parent's http block
      headers: {X-Tenant: other}
      query: {expand: null} # null removes a field
    expect: {status: 403}
```

The override block is named after the parent's type (`http`, `grpc`, `kafka`, `mongo`, `postgres`). Maps merge key by key; lists and scalars are replaced. Result ids are `<test>.<case>`.

## REST API

All under `/api`; bodies are JSON unless noted.

| Method and path | Purpose |
| --- | --- |
| `GET /apps`, `GET/PUT/DELETE /apps/{app}` | applications |
| `GET /apps/{app}/scenarios`, `GET/PUT/DELETE /apps/{app}/scenarios/{id}` | scenarios |
| `GET/PUT /apps/{app}/scenarios/{id}/yaml` | the stored file, as text |
| `GET /apps/{app}/auth`, `PUT/DELETE /apps/{app}/auth/{id}` | auth profiles, with certificate checks |
| `POST /apps/{app}/auth/{id}/check?env=` | try a profile: load certificates, fetch the token |
| `GET /apps/{app}/assets`, `PUT/DELETE /apps/{app}/assets/{certs\|protos}/{name}` | files (raw body on upload) |
| `POST /apps/{app}/run` | `{environment, scenarios, tags, kind, vars}`; returns the result |
| `GET /apps/{app}/runs`, `GET /apps/{app}/runs/{id}` | run history |
| `GET /apps/{app}/export`, `POST /import` | zip out, zip in |
