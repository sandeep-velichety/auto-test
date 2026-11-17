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
  tls?: TLSSettings
  certChecks?: CertCheck[]
}

export interface Assertion { path: string; op?: string; value?: any }

export interface Expect {
  status?: number; statusIn?: number[]; code?: string; headers?: Dict; body?: Assertion[]
  bodyContains?: string[]; count?: number; found?: boolean; maxDurationMs?: number; error?: string
}

export interface Variant {
  id: string; name: string; kind?: string; auth?: string; properties?: Dict; skip?: boolean
  http?: any; grpc?: any; kafka?: any; mongo?: any; postgres?: any
  expect?: Expect
}

export type TestType = 'http' | 'grpc' | 'kafka' | 'mongo' | 'postgres'

export interface Test {
  id: string; name: string; description?: string; type: TestType; kind?: string; auth?: string
  properties?: Dict; skip?: boolean; continueOnFail?: boolean
  retry?: { attempts: number; delayMs?: number }
  http?: any; grpc?: any; kafka?: any; mongo?: any; postgres?: any
  expect?: Expect; capture?: Dict; variants?: Variant[]
}

export interface Scenario {
  id: string; name: string; description?: string; tags?: string[]; properties?: Dict
  auth?: string; variables?: Dict; tests: Test[]
}

export interface Asset {
  category: string; name: string; size: number; modified: string; kind?: string
  subject?: string; issuer?: string; notAfter?: string; expired?: boolean; isCA?: boolean
}

export interface AssertionResult { check: string; passed: boolean; expected?: string; actual?: string }

export interface TestResult {
  id: string; name: string; variantOf?: string; type: string; kind: string; auth?: string
  properties?: Dict; status: string; attempts?: number; durationMs: number
  request?: any
  response?: { status: number; statusText?: string; headers?: Record<string, string[]>; body?: string; durationMs: number }
  assertions?: AssertionResult[]; captured?: Dict; error?: string
}

export interface ScenarioResult { id: string; name: string; status: string; durationMs: number; tests: TestResult[] | null }

export interface RunResult {
  id: string; application: string; environment?: string; startedAt: string; durationMs: number; status: string
  summary: { total: number; passed: number; failed: number; skipped: number }
  scenarios: ScenarioResult[]
}

export interface AuthCheckResult { ok: boolean; error?: string; sends?: string[]; clientCert: boolean; certChecks?: CertCheck[] }
