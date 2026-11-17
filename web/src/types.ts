export type Dict = Record<string, string>

export interface Application {
  id: string
  name: string
  description?: string
  defaultEnvironment?: string
  defaultAuth?: string
  environments?: Record<string, Dict>
}

export interface TLSSettings { cert?: string; key?: string; ca?: string; serverName?: string; insecureSkipVerify?: boolean }

export interface CertCheck { field: string; label: string; help: string; required: boolean; file?: string; status: string; detail?: string }

export interface AuthProfile {
  id: string
  name: string
  description?: string
  type: string
  basic?: { username: string; password: string }
  bearer?: { token: string; prefix?: string }
  apiKey?: { name: string; value: string; in?: string }
  oauth2?: {
    tokenUrl: string; clientId: string; clientSecret?: string; username?: string; password?: string
    scopes?: string[]; audience?: string; clientAuth?: string; extraParams?: Dict
    tokenField?: string; headerName?: string; prefix?: string
  }
  headers?: Dict
  httpCall?: { method?: string; url: string; headers?: Dict; query?: Dict; body?: any; form?: Dict; tokenPath?: string; headerName?: string; prefix?: string }
  tls?: TLSSettings
  /** Per-environment differences, merged over the fields above. */
  environments?: Record<string, Record<string, any>>
  certChecks?: CertCheck[]
}

/** Named certificate files for mutual TLS; tests pick one. */
export interface CertSet extends TLSSettings {
  id: string; name: string; description?: string
  /** Hosts this set applies to automatically (host, host:port, *.domain). */
  hosts?: string[]
  environments?: Record<string, TLSSettings>
  checks?: CertCheck[]
}

export interface Assertion { path: string; op?: string; value?: any }

export interface Expect {
  status?: number; statusIn?: number[]; code?: string; headers?: Dict; body?: Assertion[]
  bodyContains?: string[]; count?: number; found?: boolean; maxDurationMs?: number; error?: string
}

export interface Variant {
  id: string; name: string; kind?: string; auth?: string; mtls?: string; properties?: Dict; skip?: boolean
  http?: any; grpc?: any; kafka?: any; mongo?: any; postgres?: any; logs?: any
  expect?: Expect
}

export type TestType = 'http' | 'grpc' | 'kafka' | 'mongo' | 'postgres' | 'logs'

export interface Test {
  id: string; name: string; description?: string; type: TestType; kind?: string; auth?: string; mtls?: string
  properties?: Dict; skip?: boolean; continueOnFail?: boolean
  retry?: { attempts: number; delayMs?: number }
  http?: any; grpc?: any; kafka?: any; mongo?: any; postgres?: any; logs?: any
  expect?: Expect; capture?: Dict; variants?: Variant[]
}

export interface Scenario {
  id: string; name: string; description?: string; tags?: string[]; properties?: Dict
  auth?: string; mtls?: string; variables?: Dict; tests: Test[]
}

export interface Asset {
  category: string; name: string; size: number; modified: string; kind?: string
  subject?: string; issuer?: string; notAfter?: string; expired?: boolean; isCA?: boolean
}

export interface AssertionResult { check: string; passed: boolean; expected?: string; actual?: string }

export interface TestResult {
  id: string; name: string; variantOf?: string; type: string; kind: string; auth?: string; mtls?: string
  properties?: Dict; status: string; attempts?: number; durationMs: number
  request?: any
  response?: { status: number; statusText?: string; headers?: Record<string, string[]>; body?: string; durationMs: number }
  assertions?: AssertionResult[]; captured?: Dict; error?: string
}

export interface ScenarioResult { id: string; name: string; status: string; durationMs: number; tests: TestResult[] | null }

export interface RunResult {
  id: string; application: string; environment?: string; triggeredBy?: string; source?: string; startedAt: string; durationMs: number; status: string
  summary: { total: number; passed: number; failed: number; skipped: number }
  scenarios: ScenarioResult[]
}

export interface AuthCheckResult { ok: boolean; error?: string; sends?: string[]; clientCert: boolean; certChecks?: CertCheck[] }

/** A backend the UI can connect to. url '' means the server this page came from. */
export interface Backend { id: string; name: string; url: string; token?: string; user?: string }

export interface RunQuery { scenario?: string; status?: string; env?: string; from?: string; to?: string; limit?: number }

export interface GrpcMethod { name: string; fullName: string; inputType: string; outputType: string; clientStreaming?: boolean; serverStreaming?: boolean; template: any }
export interface GrpcService { name: string; methods: GrpcMethod[] }
