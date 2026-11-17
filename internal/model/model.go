// Package model defines the YAML/JSON documents that make up a workspace:
// applications, auth profiles, scenarios, tests and run results.
package model

import (
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
)

var TestTypes = []string{TypeHTTP, TypeGRPC, TypeKafka, TypeMongo, TypePostgres, TypeLogs}
var AuthTypes = []string{AuthNone, AuthBasic, AuthBearer, AuthAPIKey, AuthOAuth2ClientCredentials, AuthOAuth2Password, AuthMTLS}

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
	// TLS can accompany any type (for example OAuth2 over mTLS). For type
	// "mtls" it is required and is the only credential.
	TLS *TLSSettings `yaml:"tls,omitempty" json:"tls,omitempty"`
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
	Variables   map[string]string `yaml:"variables,omitempty" json:"variables,omitempty"`
	Tests       []Test            `yaml:"tests" json:"tests"`
}

// Test is one step. Steps run top to bottom; values captured by a step are
// available to every later step as {{name}}.
type Test struct {
	ID             string            `yaml:"id" json:"id"`
	Name           string            `yaml:"name" json:"name"`
	Description    string            `yaml:"description,omitempty" json:"description,omitempty"`
	Type           string            `yaml:"type" json:"type"`
	Kind           string            `yaml:"kind,omitempty" json:"kind,omitempty"`
	Auth           string            `yaml:"auth,omitempty" json:"auth,omitempty"`
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

func maskOne(v *string) {
	// References such as {{$env.NAME}} are not secrets themselves.
	if *v != "" && !strings.Contains(*v, "{{") {
		*v = SecretMask
	}
}

func keepOne(v *string, old string) {
	if *v == SecretMask {
		*v = old
	}
}

// Masked returns a copy with literal secret values hidden.
func (a AuthProfile) Masked() AuthProfile {
	if a.Basic != nil {
		c := *a.Basic
		maskOne(&c.Password)
		a.Basic = &c
	}
	if a.Bearer != nil {
		c := *a.Bearer
		maskOne(&c.Token)
		a.Bearer = &c
	}
	if a.APIKey != nil {
		c := *a.APIKey
		maskOne(&c.Value)
		a.APIKey = &c
	}
	if a.OAuth2 != nil {
		c := *a.OAuth2
		maskOne(&c.ClientSecret)
		maskOne(&c.Password)
		a.OAuth2 = &c
	}
	return a
}

// KeepSecrets replaces masked values in a with the ones stored in old.
func (a *AuthProfile) KeepSecrets(old *AuthProfile) {
	if old == nil {
		old = &AuthProfile{}
	}
	if a.Basic != nil {
		prev := ""
		if old.Basic != nil {
			prev = old.Basic.Password
		}
		keepOne(&a.Basic.Password, prev)
	}
	if a.Bearer != nil {
		prev := ""
		if old.Bearer != nil {
			prev = old.Bearer.Token
		}
		keepOne(&a.Bearer.Token, prev)
	}
	if a.APIKey != nil {
		prev := ""
		if old.APIKey != nil {
			prev = old.APIKey.Value
		}
		keepOne(&a.APIKey.Value, prev)
	}
	if a.OAuth2 != nil {
		var ps, pp string
		if old.OAuth2 != nil {
			ps, pp = old.OAuth2.ClientSecret, old.OAuth2.Password
		}
		keepOne(&a.OAuth2.ClientSecret, ps)
		keepOne(&a.OAuth2.Password, pp)
	}
}

// Validate checks an auth profile and reports what is missing.
func (a *AuthProfile) Validate() []string {
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
	case AuthOAuth2ClientCredentials:
		if a.OAuth2 == nil || a.OAuth2.TokenURL == "" || a.OAuth2.ClientID == "" {
			add("oauth2.tokenUrl and oauth2.clientId are required")
		}
	case AuthOAuth2Password:
		if a.OAuth2 == nil || a.OAuth2.TokenURL == "" || a.OAuth2.ClientID == "" || a.OAuth2.Username == "" {
			add("oauth2.tokenUrl, oauth2.clientId and oauth2.username are required")
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
