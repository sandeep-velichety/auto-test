// Package model defines the YAML/JSON documents that make up a workspace:
// applications, auth profiles, scenarios, tests and run results.
package model

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Test step types.
const (
	TypeHTTP     = "http"
	TypeGRPC     = "grpc"
	TypeKafka    = "kafka"
	TypeMongo    = "mongo"
	TypePostgres = "postgres"
	TypeLogs     = "logs"
)

// Test kinds.
const (
	KindPositive = "positive"
	KindNegative = "negative"
)

// Auth profile types.
const (
	AuthNone                    = "none"
	AuthBasic                   = "basic"
	AuthBearer                  = "bearer"
	AuthAPIKey                  = "api_key"
	AuthOAuth2ClientCredentials = "oauth2_client_credentials"
	AuthOAuth2Password          = "oauth2_password"
	AuthMTLS                    = "mtls"
	AuthHeaders                 = "headers"
	AuthHTTPCall                = "http_call"
)

var TestTypes = []string{TypeHTTP, TypeGRPC, TypeKafka, TypeMongo, TypePostgres, TypeLogs}
var AuthTypes = []string{AuthNone, AuthBasic, AuthBearer, AuthAPIKey, AuthHeaders, AuthOAuth2ClientCredentials, AuthOAuth2Password, AuthHTTPCall, AuthMTLS}

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
var fileRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// ValidID reports whether s is safe to use as an id and a file name stem.
func ValidID(s string) bool { return idRe.MatchString(s) }

// ValidFileName reports whether s is a plain asset file name (no path parts).
func ValidFileName(s string) bool { return fileRe.MatchString(s) }

// Application is one system under test. It lives in
// <workspace>/applications/<id>/application.yaml with its assets and
// scenarios next to it.
type Application struct {
	ID                 string                       `yaml:"id" json:"id"`
	Name               string                       `yaml:"name" json:"name"`
	Description        string                       `yaml:"description,omitempty" json:"description,omitempty"`
	DefaultEnvironment string                       `yaml:"defaultEnvironment,omitempty" json:"defaultEnvironment,omitempty"`
	DefaultAuth        string                       `yaml:"defaultAuth,omitempty" json:"defaultAuth,omitempty"`
	Environments       map[string]map[string]string `yaml:"environments,omitempty" json:"environments,omitempty"`
}

// AuthProfile is stored in <app>/assets/auth/<id>.yaml.
type AuthProfile struct {
	ID          string      `yaml:"id" json:"id"`
	Name        string      `yaml:"name" json:"name"`
	Description string      `yaml:"description,omitempty" json:"description,omitempty"`
	Type        string      `yaml:"type" json:"type"`
	Basic       *BasicAuth  `yaml:"basic,omitempty" json:"basic,omitempty"`
	Bearer      *BearerAuth `yaml:"bearer,omitempty" json:"bearer,omitempty"`
	APIKey      *APIKeyAuth `yaml:"apiKey,omitempty" json:"apiKey,omitempty"`
	OAuth2      *OAuth2Auth `yaml:"oauth2,omitempty" json:"oauth2,omitempty"`
	// Headers are sent with every request, on top of what the type adds.
	// Type "headers" is nothing but these.
	Headers map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
	// HTTPCall fetches a token with a request you describe (type http_call).
	HTTPCall *HTTPCallAuth `yaml:"httpCall,omitempty" json:"httpCall,omitempty"`
	// TLS can accompany any type (for example OAuth2 over mTLS). For type
	// "mtls" it is required and is the only credential. A certificate set
	// chosen on the test takes its place.
	TLS *TLSSettings `yaml:"tls,omitempty" json:"tls,omitempty"`
	// Environments holds per-environment differences, deep-merged over the
	// fields above when a run uses that environment, for example
	//   environments: {staging: {oauth2: {clientId: qa-staging}}}
	Environments map[string]map[string]any `yaml:"environments,omitempty" json:"environments,omitempty"`
}

// HTTPCallAuth describes a custom token request: any method, headers and
// body. The token is read from the JSON response at TokenPath and sent on
// each test request in HeaderName.
type HTTPCallAuth struct {
	Method     string            `yaml:"method,omitempty" json:"method,omitempty"` // default POST
	URL        string            `yaml:"url" json:"url"`
	Headers    map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
	Query      map[string]string `yaml:"query,omitempty" json:"query,omitempty"`
	Body       any               `yaml:"body,omitempty" json:"body,omitempty"`
	Form       map[string]string `yaml:"form,omitempty" json:"form,omitempty"`
	TokenPath  string            `yaml:"tokenPath,omitempty" json:"tokenPath,omitempty"`   // default access_token
	HeaderName string            `yaml:"headerName,omitempty" json:"headerName,omitempty"` // default Authorization
	Prefix     string            `yaml:"prefix,omitempty" json:"prefix,omitempty"`         // default Bearer; "none" for the bare token
}

// CertSet is a named set of certificate files for mutual TLS, stored in
// <app>/assets/tls/<id>.yaml. Tests choose one with "mtls: <id>".
type CertSet struct {
	ID          string `yaml:"id" json:"id"`
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	// Hosts makes the set apply automatically, the way client certificates
	// work in Postman: a call to a matching host presents these files unless
	// the test says otherwise. Entries are host or host:port; "*.example.com"
	// matches any subdomain. Variables such as {{ordersHost}} are allowed.
	Hosts              []string `yaml:"hosts,omitempty" json:"hosts,omitempty"`
	Cert               string   `yaml:"cert,omitempty" json:"cert,omitempty"`
	Key                string   `yaml:"key,omitempty" json:"key,omitempty"`
	CA                 string   `yaml:"ca,omitempty" json:"ca,omitempty"`
	ServerName         string   `yaml:"serverName,omitempty" json:"serverName,omitempty"`
	InsecureSkipVerify bool     `yaml:"insecureSkipVerify,omitempty" json:"insecureSkipVerify,omitempty"`
	// Environments replaces individual fields for one environment, so
	// staging can use different files from dev.
	Environments map[string]TLSSettings `yaml:"environments,omitempty" json:"environments,omitempty"`
}

// MatchesHost reports whether a call to hostport (host or host:port) should
// use this set, given its already-rendered host patterns.
func MatchesHost(patterns []string, hostport string) bool {
	host, port := splitHostPort(strings.ToLower(strings.TrimSpace(hostport)))
	if host == "" {
		return false
	}
	for _, p := range patterns {
		ph, pp := splitHostPort(strings.ToLower(strings.TrimSpace(p)))
		if ph == "" || (pp != "" && pp != port) {
			continue
		}
		if ph == host || (strings.HasPrefix(ph, "*.") && strings.HasSuffix(host, ph[1:]) && len(host) > len(ph)-1) {
			return true
		}
	}
	return false
}

func splitHostPort(s string) (host, port string) {
	if i := strings.LastIndex(s, ":"); i > 0 && !strings.Contains(s[i+1:], "]") && !strings.Contains(s[:i], ":") {
		return s[:i], s[i+1:]
	}
	return s, ""
}

// For returns the files to use in an environment.
func (c *CertSet) For(env string) *TLSSettings {
	t := TLSSettings{Cert: c.Cert, Key: c.Key, CA: c.CA, ServerName: c.ServerName, InsecureSkipVerify: c.InsecureSkipVerify}
	if o, ok := c.Environments[env]; ok {
		if o.Cert != "" {
			t.Cert = o.Cert
		}
		if o.Key != "" {
			t.Key = o.Key
		}
		if o.CA != "" {
			t.CA = o.CA
		}
		if o.ServerName != "" {
			t.ServerName = o.ServerName
		}
		t.InsecureSkipVerify = t.InsecureSkipVerify || o.InsecureSkipVerify
	}
	return &t
}

// Validate checks a certificate set, including every environment's result.
func (c *CertSet) Validate() []string {
	var errs []string
	if !ValidID(c.ID) {
		errs = append(errs, "id must be lowercase letters, digits, '-' or '_'")
	}
	check := func(where string, t *TLSSettings) {
		if (t.Cert == "") != (t.Key == "") {
			errs = append(errs, where+"client certificate and private key must be set together")
		}
		if t.Cert == "" && t.CA == "" {
			errs = append(errs, where+"choose a client certificate and key, or at least a CA bundle")
		}
		for _, f := range []string{t.Cert, t.Key, t.CA} {
			if f != "" && !ValidFileName(f) {
				errs = append(errs, fmt.Sprintf("%sfile %q must be a plain file name in assets/certs", where, f))
			}
		}
	}
	check("", c.For(""))
	for env := range c.Environments {
		check("environment "+env+": ", c.For(env))
	}
	return errs
}

type BasicAuth struct {
	Username string `yaml:"username" json:"username"`
	Password string `yaml:"password" json:"password"`
}

type BearerAuth struct {
	Token  string `yaml:"token" json:"token"`
	Prefix string `yaml:"prefix,omitempty" json:"prefix,omitempty"` // default "Bearer"
}

type APIKeyAuth struct {
	Name  string `yaml:"name" json:"name"`
	Value string `yaml:"value" json:"value"`
	In    string `yaml:"in,omitempty" json:"in,omitempty"` // header (default) | query
}

type OAuth2Auth struct {
	TokenURL     string            `yaml:"tokenUrl" json:"tokenUrl"`
	ClientID     string            `yaml:"clientId" json:"clientId"`
	ClientSecret string            `yaml:"clientSecret,omitempty" json:"clientSecret,omitempty"`
	Username     string            `yaml:"username,omitempty" json:"username,omitempty"`
	Password     string            `yaml:"password,omitempty" json:"password,omitempty"`
	Scopes       []string          `yaml:"scopes,omitempty" json:"scopes,omitempty"`
	Audience     string            `yaml:"audience,omitempty" json:"audience,omitempty"`
	ClientAuth   string            `yaml:"clientAuth,omitempty" json:"clientAuth,omitempty"` // body (default) | basic
	ExtraParams  map[string]string `yaml:"extraParams,omitempty" json:"extraParams,omitempty"`
	TokenField   string            `yaml:"tokenField,omitempty" json:"tokenField,omitempty"` // default access_token
	HeaderName   string            `yaml:"headerName,omitempty" json:"headerName,omitempty"` // default Authorization
	Prefix       string            `yaml:"prefix,omitempty" json:"prefix,omitempty"`         // default Bearer
}

// TLSSettings names files in <app>/assets/certs.
type TLSSettings struct {
	Cert               string `yaml:"cert,omitempty" json:"cert,omitempty"`
	Key                string `yaml:"key,omitempty" json:"key,omitempty"`
	CA                 string `yaml:"ca,omitempty" json:"ca,omitempty"`
	ServerName         string `yaml:"serverName,omitempty" json:"serverName,omitempty"`
	InsecureSkipVerify bool   `yaml:"insecureSkipVerify,omitempty" json:"insecureSkipVerify,omitempty"`
}

// Scenario is an ordered list of tests, stored in <app>/scenarios/<id>.yaml.
type Scenario struct {
	ID          string            `yaml:"id" json:"id"`
	Name        string            `yaml:"name" json:"name"`
	Description string            `yaml:"description,omitempty" json:"description,omitempty"`
	Tags        []string          `yaml:"tags,omitempty" json:"tags,omitempty"`
	Properties  map[string]string `yaml:"properties,omitempty" json:"properties,omitempty"`
	Auth        string            `yaml:"auth,omitempty" json:"auth,omitempty"`
	MTLS        string            `yaml:"mtls,omitempty" json:"mtls,omitempty"` // default certificate set for every step
	Variables   map[string]string `yaml:"variables,omitempty" json:"variables,omitempty"`
	Tests       []Test            `yaml:"tests" json:"tests"`
}

// Test is one step. Steps run top to bottom; values captured by a step are
// available to every later step as {{name}}.
type Test struct {
	ID          string `yaml:"id" json:"id"`
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	Type        string `yaml:"type" json:"type"`
	Kind        string `yaml:"kind,omitempty" json:"kind,omitempty"`
	Auth        string `yaml:"auth,omitempty" json:"auth,omitempty"`
	// MTLS names a certificate set to present (mutual TLS); "none" turns it
	// off; empty inherits from the scenario.
	MTLS           string            `yaml:"mtls,omitempty" json:"mtls,omitempty"`
	Properties     map[string]string `yaml:"properties,omitempty" json:"properties,omitempty"`
	Skip           bool              `yaml:"skip,omitempty" json:"skip,omitempty"`
	ContinueOnFail bool              `yaml:"continueOnFail,omitempty" json:"continueOnFail,omitempty"`
	Retry          *Retry            `yaml:"retry,omitempty" json:"retry,omitempty"`

	HTTP     *HTTPStep     `yaml:"http,omitempty" json:"http,omitempty"`
	GRPC     *GRPCStep     `yaml:"grpc,omitempty" json:"grpc,omitempty"`
	Kafka    *KafkaStep    `yaml:"kafka,omitempty" json:"kafka,omitempty"`
	Mongo    *MongoStep    `yaml:"mongo,omitempty" json:"mongo,omitempty"`
	Postgres *PostgresStep `yaml:"postgres,omitempty" json:"postgres,omitempty"`
	Logs     *LogsStep     `yaml:"logs,omitempty" json:"logs,omitempty"`

	Expect   *Expect           `yaml:"expect,omitempty" json:"expect,omitempty"`
	Capture  map[string]string `yaml:"capture,omitempty" json:"capture,omitempty"`
	Variants []Variant         `yaml:"variants,omitempty" json:"variants,omitempty"`
}

// Variant re-runs its parent test with some fields overridden. It is how a
// negative case is paired with the happy path: only the differences are
// written down. Variants run right after their parent, in order, and never
// feed captures to later steps.
type Variant struct {
	ID         string            `yaml:"id" json:"id"`
	Name       string            `yaml:"name" json:"name"`
	Kind       string            `yaml:"kind,omitempty" json:"kind,omitempty"` // default negative
	Auth       string            `yaml:"auth,omitempty" json:"auth,omitempty"`
	MTLS       string            `yaml:"mtls,omitempty" json:"mtls,omitempty"`
	Properties map[string]string `yaml:"properties,omitempty" json:"properties,omitempty"`
	Skip       bool              `yaml:"skip,omitempty" json:"skip,omitempty"`

	// Overrides are deep-merged over the parent's block of the same type.
	HTTP     map[string]any `yaml:"http,omitempty" json:"http,omitempty"`
	GRPC     map[string]any `yaml:"grpc,omitempty" json:"grpc,omitempty"`
	Kafka    map[string]any `yaml:"kafka,omitempty" json:"kafka,omitempty"`
	Mongo    map[string]any `yaml:"mongo,omitempty" json:"mongo,omitempty"`
	Postgres map[string]any `yaml:"postgres,omitempty" json:"postgres,omitempty"`
	Logs     map[string]any `yaml:"logs,omitempty" json:"logs,omitempty"`

	Expect *Expect `yaml:"expect,omitempty" json:"expect,omitempty"`
}

type Retry struct {
	Attempts int `yaml:"attempts" json:"attempts"`
	DelayMs  int `yaml:"delayMs,omitempty" json:"delayMs,omitempty"`
}

type HTTPStep struct {
	Method          string            `yaml:"method" json:"method"`
	URL             string            `yaml:"url" json:"url"`
	Headers         map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
	Query           map[string]string `yaml:"query,omitempty" json:"query,omitempty"`
	Body            any               `yaml:"body,omitempty" json:"body,omitempty"`
	Form            map[string]string `yaml:"form,omitempty" json:"form,omitempty"`
	TimeoutMs       int               `yaml:"timeoutMs,omitempty" json:"timeoutMs,omitempty"`
	FollowRedirects bool              `yaml:"followRedirects,omitempty" json:"followRedirects,omitempty"`
}

type GRPCStep struct {
	Target     string            `yaml:"target" json:"target"`
	Method     string            `yaml:"method" json:"method"` // package.Service/Method
	Metadata   map[string]string `yaml:"metadata,omitempty" json:"metadata,omitempty"`
	Message    any               `yaml:"message,omitempty" json:"message,omitempty"`
	Plaintext  bool              `yaml:"plaintext,omitempty" json:"plaintext,omitempty"`
	ProtoFiles []string          `yaml:"protoFiles,omitempty" json:"protoFiles,omitempty"` // files in assets/protos; empty = server reflection
	TimeoutMs  int               `yaml:"timeoutMs,omitempty" json:"timeoutMs,omitempty"`
}

type KafkaStep struct {
	Brokers string            `yaml:"brokers" json:"brokers"` // comma separated
	Topic   string            `yaml:"topic" json:"topic"`
	Action  string            `yaml:"action,omitempty" json:"action,omitempty"` // consume (default) | produce
	Key     string            `yaml:"key,omitempty" json:"key,omitempty"`
	Value   any               `yaml:"value,omitempty" json:"value,omitempty"`
	Headers map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
	// Match selects the message a consume step waits for.
	Match []Assertion `yaml:"match,omitempty" json:"match,omitempty"`
	// From: scenario_start (default) reads everything published since the
	// scenario began, so a consume step placed after the call that emits
	// the event still sees it. earliest reads the whole topic.
	From      string `yaml:"from,omitempty" json:"from,omitempty"`
	SASL      string `yaml:"sasl,omitempty" json:"sasl,omitempty"` // "", plain, scram-sha-256, scram-sha-512 (credentials from a basic auth profile)
	TLS       bool   `yaml:"tls,omitempty" json:"tls,omitempty"`
	TimeoutMs int    `yaml:"timeoutMs,omitempty" json:"timeoutMs,omitempty"`
}

type MongoStep struct {
	URI        string `yaml:"uri" json:"uri"`
	Database   string `yaml:"database" json:"database"`
	Collection string `yaml:"collection" json:"collection"`
	Operation  string `yaml:"operation,omitempty" json:"operation,omitempty"` // find (default) | count | aggregate
	Filter     any    `yaml:"filter,omitempty" json:"filter,omitempty"`
	Pipeline   []any  `yaml:"pipeline,omitempty" json:"pipeline,omitempty"`
	Sort       any    `yaml:"sort,omitempty" json:"sort,omitempty"`
	Limit      int    `yaml:"limit,omitempty" json:"limit,omitempty"`
	TimeoutMs  int    `yaml:"timeoutMs,omitempty" json:"timeoutMs,omitempty"`
}

type PostgresStep struct {
	DSN       string `yaml:"dsn" json:"dsn"`
	Query     string `yaml:"query" json:"query"`
	Args      []any  `yaml:"args,omitempty" json:"args,omitempty"`
	TimeoutMs int    `yaml:"timeoutMs,omitempty" json:"timeoutMs,omitempty"`
}

// LogsStep searches a log platform for entries written while the scenario
// ran, so a test can assert on what the service itself logged.
type LogsStep struct {
	Provider string `yaml:"provider,omitempty" json:"provider,omitempty"` // datadog (default)
	Site     string `yaml:"site,omitempty" json:"site,omitempty"`         // datadoghq.com (default), datadoghq.eu, us5.datadoghq.com ...
	URL      string `yaml:"url,omitempty" json:"url,omitempty"`           // API base URL; overrides site
	Query    string `yaml:"query" json:"query"`                           // provider search syntax, e.g. service:orders @orderId:{{orderId}}
	// From: scenario_start (default) searches from the moment the scenario
	// began. Anything else is passed to the provider (now-15m, an ISO time).
	From      string `yaml:"from,omitempty" json:"from,omitempty"`
	To        string `yaml:"to,omitempty" json:"to,omitempty"` // default now
	Limit     int    `yaml:"limit,omitempty" json:"limit,omitempty"`
	APIKey    string `yaml:"apiKey,omitempty" json:"apiKey,omitempty"` // use {{$env.DD_API_KEY}}
	AppKey    string `yaml:"appKey,omitempty" json:"appKey,omitempty"` // use {{$env.DD_APP_KEY}}
	TimeoutMs int    `yaml:"timeoutMs,omitempty" json:"timeoutMs,omitempty"`
}

// Expect describes what a passing result looks like. When it is omitted a
// positive test expects success (HTTP 2xx, gRPC OK, query ran, message
// found) and a negative test expects the opposite.
type Expect struct {
	Status        *int              `yaml:"status,omitempty" json:"status,omitempty"`               // HTTP status
	StatusIn      []int             `yaml:"statusIn,omitempty" json:"statusIn,omitempty"`           // any of
	Code          string            `yaml:"code,omitempty" json:"code,omitempty"`                   // gRPC code name, e.g. NOT_FOUND
	Headers       map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`             // header equals
	Body          []Assertion       `yaml:"body,omitempty" json:"body,omitempty"`                   // checks on the JSON result
	BodyContains  []string          `yaml:"bodyContains,omitempty" json:"bodyContains,omitempty"`   // raw substring checks
	Count         *int              `yaml:"count,omitempty" json:"count,omitempty"`                 // rows / documents / log entries
	Found         *bool             `yaml:"found,omitempty" json:"found,omitempty"`                 // kafka: message arrived or not
	MaxDurationMs int               `yaml:"maxDurationMs,omitempty" json:"maxDurationMs,omitempty"` // latency budget
	// Error expects the call to fail before any response arrives (refused
	// TLS handshake, connection reset) with a message containing this text.
	// "*" accepts any such failure.
	Error string `yaml:"error,omitempty" json:"error,omitempty"`
}

// Assertion checks one value in the JSON result. Path uses gjson syntax
// (items.0.id, rows.#, user.name).
type Assertion struct {
	Path  string `yaml:"path" json:"path"`
	Op    string `yaml:"op,omitempty" json:"op,omitempty"` // equals (default), not_equals, contains, matches, exists, not_exists, gt, gte, lt, lte, length, in
	Value any    `yaml:"value,omitempty" json:"value,omitempty"`
}

// Validate checks the structure of a scenario without running it.
func (s *Scenario) Validate() []string {
	var errs []string
	add := func(f string, a ...any) { errs = append(errs, fmt.Sprintf(f, a...)) }
	if !ValidID(s.ID) {
		add("scenario id %q must be lowercase letters, digits, '-' or '_'", s.ID)
	}
	if s.Name == "" {
		add("scenario name is required")
	}
	seen := map[string]bool{}
	for i := range s.Tests {
		t := &s.Tests[i]
		where := fmt.Sprintf("test #%d (%s)", i+1, t.ID)
		if !ValidID(t.ID) {
			add("%s: id must be lowercase letters, digits, '-' or '_'", where)
		}
		if seen[t.ID] {
			add("%s: duplicate test id", where)
		}
		seen[t.ID] = true
		if t.Kind != "" && t.Kind != KindPositive && t.Kind != KindNegative {
			add("%s: kind must be positive or negative", where)
		}
		switch t.Type {
		case TypeHTTP:
			if t.HTTP == nil || t.HTTP.URL == "" {
				add("%s: http.url is required", where)
			}
		case TypeGRPC:
			if t.GRPC == nil || t.GRPC.Target == "" || t.GRPC.Method == "" {
				add("%s: grpc.target and grpc.method are required", where)
			}
		case TypeKafka:
			if t.Kafka == nil || t.Kafka.Brokers == "" || t.Kafka.Topic == "" {
				add("%s: kafka.brokers and kafka.topic are required", where)
			}
		case TypeMongo:
			if t.Mongo == nil || t.Mongo.URI == "" || t.Mongo.Database == "" || t.Mongo.Collection == "" {
				add("%s: mongo.uri, mongo.database and mongo.collection are required", where)
			}
		case TypePostgres:
			if t.Postgres == nil || t.Postgres.DSN == "" || t.Postgres.Query == "" {
				add("%s: postgres.dsn and postgres.query are required", where)
			}
		case TypeLogs:
			if t.Logs == nil || t.Logs.Query == "" {
				add("%s: logs.query is required", where)
			}
		default:
			add("%s: type must be one of %v", where, TestTypes)
		}
		vseen := map[string]bool{}
		for _, v := range t.Variants {
			if !ValidID(v.ID) {
				add("%s: variant id %q is invalid", where, v.ID)
			}
			if vseen[v.ID] {
				add("%s: duplicate variant id %q", where, v.ID)
			}
			vseen[v.ID] = true
			if v.Kind != "" && v.Kind != KindPositive && v.Kind != KindNegative {
				add("%s: variant %q kind must be positive or negative", where, v.ID)
			}
		}
	}
	return errs
}

// SecretMask stands in for a stored secret in API responses. Sending it
// back unchanged means "keep the stored value".
const SecretMask = "********"

// secretKeys are profile fields whose values are secrets. Every value
// under "headers" and "form" is treated as one too.
var secretKeys = map[string]bool{"password": true, "token": true, "value": true, "clientSecret": true}

func isSecret(key, parent string) bool {
	return secretKeys[key] || parent == "headers" || parent == "form"
}

func walkSecrets(m, old map[string]any, parent string, fn func(v string, prev any) string) {
	for k, v := range m {
		switch x := v.(type) {
		case string:
			if isSecret(k, parent) {
				var prev any
				if old != nil {
					prev = old[k]
				}
				m[k] = fn(x, prev)
			}
		case map[string]any:
			var o map[string]any
			if old != nil {
				o, _ = old[k].(map[string]any)
			}
			walkSecrets(x, o, k, fn)
		}
	}
}

func profileMap(a *AuthProfile) map[string]any {
	m := map[string]any{}
	if a == nil {
		return m
	}
	b, _ := json.Marshal(a)
	_ = json.Unmarshal(b, &m)
	return m
}

// Masked returns a copy with literal secret values hidden, including those
// in per-environment overrides.
func (a AuthProfile) Masked() AuthProfile {
	m := profileMap(&a)
	walkSecrets(m, nil, "", func(v string, _ any) string {
		// References such as {{$env.NAME}} are not secrets themselves.
		if v == "" || strings.Contains(v, "{{") {
			return v
		}
		return SecretMask
	})
	var out AuthProfile
	b, _ := json.Marshal(m)
	_ = json.Unmarshal(b, &out)
	return out
}

// KeepSecrets replaces masked values in a with the ones stored in old. A
// masked value inside an environment override falls back to what that
// environment used before, which may be the default value.
func (a *AuthProfile) KeepSecrets(old *AuthProfile) {
	keep := func(v string, prev any) string {
		if v != SecretMask {
			return v
		}
		p, _ := prev.(string)
		return p
	}
	m, oldM := profileMap(a), profileMap(old)
	envs, _ := m["environments"].(map[string]any)
	oldEnvs, _ := oldM["environments"].(map[string]any)
	delete(m, "environments")
	delete(oldM, "environments")
	walkSecrets(m, oldM, "", keep)
	for name, o := range envs {
		override, ok := o.(map[string]any)
		if !ok {
			continue
		}
		oldOverride, _ := oldEnvs[name].(map[string]any)
		walkSecrets(override, mergeMaps(oldM, oldOverride), "", keep)
	}
	if len(envs) > 0 {
		m["environments"] = envs
	}
	var out AuthProfile
	b, _ := json.Marshal(m)
	if json.Unmarshal(b, &out) == nil {
		*a = out
	}
}

// mergeMaps lays override over base: maps merge key by key, anything else
// is replaced, null removes.
func mergeMaps(base, override map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(override))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range override {
		if v == nil {
			delete(out, k)
			continue
		}
		bm, ok1 := out[k].(map[string]any)
		om, ok2 := v.(map[string]any)
		if ok1 && ok2 {
			out[k] = mergeMaps(bm, om)
		} else {
			out[k] = v
		}
	}
	return out
}

// ForEnv returns the profile as it applies in one environment.
func (a *AuthProfile) ForEnv(env string) (*AuthProfile, error) {
	m := profileMap(a)
	delete(m, "environments")
	if o, ok := a.Environments[env]; ok {
		ob, _ := json.Marshal(o)
		var om map[string]any
		_ = json.Unmarshal(ob, &om)
		delete(om, "id")
		delete(om, "environments")
		m = mergeMaps(m, om)
	}
	var out AuthProfile
	b, _ := json.Marshal(m)
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("environment %s: %w", env, err)
	}
	out.ID = a.ID
	return &out, nil
}

// Validate checks an auth profile, including how it reads in every
// environment that overrides something.
func (a *AuthProfile) Validate() []string {
	errs := a.validateOne()
	for env := range a.Environments {
		eff, err := a.ForEnv(env)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		for _, e := range eff.validateOne() {
			errs = append(errs, "environment "+env+": "+e)
		}
	}
	return errs
}

func (a *AuthProfile) validateOne() []string {
	var errs []string
	add := func(s string) { errs = append(errs, s) }
	if !ValidID(a.ID) {
		add("id must be lowercase letters, digits, '-' or '_'")
	}
	switch a.Type {
	case AuthNone:
	case AuthBasic:
		if a.Basic == nil || a.Basic.Username == "" {
			add("basic.username is required")
		}
	case AuthBearer:
		if a.Bearer == nil || a.Bearer.Token == "" {
			add("bearer.token is required")
		}
	case AuthAPIKey:
		if a.APIKey == nil || a.APIKey.Name == "" || a.APIKey.Value == "" {
			add("apiKey.name and apiKey.value are required")
		}
	case AuthHeaders:
		if len(a.Headers) == 0 {
			add("add at least one header")
		}
	case AuthOAuth2ClientCredentials:
		if a.OAuth2 == nil || a.OAuth2.TokenURL == "" || a.OAuth2.ClientID == "" {
			add("oauth2.tokenUrl and oauth2.clientId are required")
		}
	case AuthOAuth2Password:
		if a.OAuth2 == nil || a.OAuth2.TokenURL == "" || a.OAuth2.ClientID == "" || a.OAuth2.Username == "" {
			add("oauth2.tokenUrl, oauth2.clientId and oauth2.username are required")
		}
	case AuthHTTPCall:
		if a.HTTPCall == nil || a.HTTPCall.URL == "" {
			add("httpCall.url is required")
		}
	case AuthMTLS:
		if a.TLS == nil || a.TLS.Cert == "" || a.TLS.Key == "" {
			add("mTLS needs tls.cert (client certificate) and tls.key (client private key)")
		}
	default:
		add(fmt.Sprintf("type must be one of %v", AuthTypes))
	}
	if a.TLS != nil {
		if (a.TLS.Cert == "") != (a.TLS.Key == "") {
			add("tls.cert and tls.key must be set together")
		}
		for _, f := range []string{a.TLS.Cert, a.TLS.Key, a.TLS.CA} {
			if f != "" && !ValidFileName(f) {
				add(fmt.Sprintf("tls file %q must be a plain file name in assets/certs", f))
			}
		}
	}
	return errs
}
