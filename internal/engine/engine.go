package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"

	"github.com/testplane/testplane/internal/assert"
	"github.com/testplane/testplane/internal/auth"
	"github.com/testplane/testplane/internal/executor"
	"github.com/testplane/testplane/internal/model"
	"github.com/testplane/testplane/internal/store"
)

// Options selects what to run.
type Options struct {
	App       string
	Env       string            // empty = the application's default environment
	Scenarios []string          // empty = all
	Tags      []string          // any-of filter on scenario tags
	Kind      string            // "", positive (skip negative cases) or negative (skip positive variants)
	Vars      map[string]string // overrides environment and scenario variables
	Until     string            // stop each scenario after this test id (and its cases); for trying a step while authoring
	By        string            // who started the run, recorded with it
	Source    string            // server | cli
	// OnTest is called as each test finishes (for live CLI output).
	OnTest func(scenario *model.Scenario, r *model.TestResult)
}

type Engine struct{ Store *store.Store }

func New(s *store.Store) *Engine { return &Engine{Store: s} }

const maxStoredBody = 64 << 10

// Run executes the selected scenarios one after another.
func (e *Engine) Run(ctx context.Context, o Options) (*model.RunResult, error) {
	app, err := e.Store.GetApp(o.App)
	if err != nil {
		return nil, fmt.Errorf("application %q: %w", o.App, err)
	}
	envName := o.Env
	if envName == "" {
		envName = app.DefaultEnvironment
	}
	base := map[string]string{}
	if envName != "" {
		env, ok := app.Environments[envName]
		if !ok {
			return nil, fmt.Errorf("%w: application %q has no environment %q", store.ErrInvalid, app.ID, envName)
		}
		for k, v := range env {
			base[k] = v
		}
	}
	// Other applications' environment values are visible as {{app-id.name}},
	// so a flow scenario can call several services. Each one contributes the
	// environment with the same name, or else its own default.
	appEnv := map[string]map[string]string{app.ID: copyVars(base)}
	if others, err := e.Store.ListApps(); err == nil {
		for _, o := range others {
			if o.ID == app.ID {
				continue
			}
			env, ok := o.Environments[envName]
			if !ok {
				env = o.Environments[o.DefaultEnvironment]
			}
			appEnv[o.ID] = copyVars(env)
			for k, v := range env {
				base[o.ID+"."+k] = v
			}
		}
	}
	all, err := e.Store.ListScenarios(app.ID)
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, id := range o.Scenarios {
		want[id] = true
	}
	filtered := len(o.Scenarios) > 0
	var selected []model.Scenario
	for _, sc := range all {
		if filtered && !want[sc.ID] {
			continue
		}
		delete(want, sc.ID)
		if len(o.Tags) > 0 && !anyTag(sc.Tags, o.Tags) {
			continue
		}
		selected = append(selected, sc)
	}
	for id := range want {
		return nil, fmt.Errorf("%w: scenario %q not found in application %q", store.ErrNotFound, id, app.ID)
	}

	start := time.Now()
	suffix := make([]byte, 3)
	_, _ = rand.Read(suffix)
	run := &model.RunResult{
		ID:          start.UTC().Format("20060102-150405") + "-" + hex.EncodeToString(suffix),
		Application: app.ID, Environment: envName, StartedAt: start.UTC(), TriggeredBy: o.By, Source: o.Source,
		Status: model.StatusPassed, Scenarios: []model.ScenarioResult{},
	}
	shared := &runShared{store: e.Store, appEnv: appEnv, env: envName, resolver: auth.NewResolver("")}
	for i := range selected {
		sr := e.runScenario(ctx, app, &selected[i], base, shared, o)
		run.Scenarios = append(run.Scenarios, sr)
		for _, t := range sr.Tests {
			run.Summary.Total++
			switch t.Status {
			case model.StatusPassed:
				run.Summary.Passed++
			case model.StatusSkipped:
				run.Summary.Skipped++
			default:
				run.Summary.Failed++
			}
		}
	}
	if run.Summary.Failed > 0 {
		run.Status = model.StatusFailed
	}
	run.DurationMs = time.Since(start).Milliseconds()
	return run, nil
}

func copyVars(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// runShared is state kept for a whole run: the credential resolver (so
// tokens are fetched once), the environment being run and every
// application's environment values.
type runShared struct {
	store    *store.Store
	appEnv   map[string]map[string]string
	env      string
	resolver *auth.Resolver
}

func anyTag(have, want []string) bool {
	for _, h := range have {
		for _, w := range want {
			if strings.EqualFold(h, w) {
				return true
			}
		}
	}
	return false
}

// spec is a base test or one of its variants, flattened to what runOne needs.
type spec struct {
	id, name, variantOf string
	typ, kind, authID   string
	mtlsID              string
	props               map[string]string
	block               map[string]any
	expect              *model.Expect
	retry               *model.Retry
	capture             map[string]string
}

type scenarioRun struct {
	e       *Engine
	app     *model.Application
	sc      *model.Scenario
	vars    map[string]string
	shared  *runShared
	kafka   *executor.KafkaSession
	started time.Time
	sets    []model.CertSet // this application's certificate sets, loaded once
	setsOK  bool
}

// matchSet finds a certificate set of this application whose host patterns
// cover the host a step is about to call.
func (r *scenarioRun) matchSet(host string) string {
	if host == "" {
		return ""
	}
	if !r.setsOK {
		r.sets, _ = r.e.Store.ListCertSets(r.app.ID)
		r.setsOK = true
	}
	for _, c := range r.sets {
		if len(c.Hosts) == 0 {
			continue
		}
		rd := newRenderer(r.vars)
		patterns := make([]string, len(c.Hosts))
		for i, h := range c.Hosts {
			patterns[i] = rd.str(h)
		}
		if model.MatchesHost(patterns, host) {
			return c.ID
		}
	}
	return ""
}

// targetHost returns the host:port a rendered step will call, for the step
// types where a certificate can be matched by host.
func targetHost(typ string, block map[string]any) string {
	switch typ {
	case model.TypeHTTP, model.TypeLogs:
		raw, _ := block["url"].(string)
		if u, err := url.Parse(raw); err == nil {
			return u.Host
		}
	case model.TypeGRPC:
		t, _ := block["target"].(string)
		return t
	}
	return ""
}

func (e *Engine) runScenario(ctx context.Context, app *model.Application, sc *model.Scenario, base map[string]string, shared *runShared, o Options) model.ScenarioResult {
	start := time.Now()
	res := model.ScenarioResult{ID: sc.ID, Name: sc.Name, Status: model.StatusPassed, Tests: []model.TestResult{}}
	emit := func(r model.TestResult) {
		res.Tests = append(res.Tests, r)
		if r.Status == model.StatusFailed || r.Status == model.StatusError {
			res.Status = model.StatusFailed
		}
		if o.OnTest != nil {
			o.OnTest(sc, &r)
		}
	}
	if strings.HasPrefix(sc.Description, "INVALID: ") {
		emit(model.TestResult{ID: sc.ID, Name: "load scenario", Status: model.StatusError, Error: strings.TrimPrefix(sc.Description, "INVALID: ")})
		return res
	}
	if errs := sc.Validate(); len(errs) > 0 {
		emit(model.TestResult{ID: sc.ID, Name: "validate scenario", Status: model.StatusError, Error: strings.Join(errs, "; ")})
		return res
	}

	// Variable precedence, lowest first: environment, scenario, run
	// overrides, then values captured by earlier steps.
	vars := map[string]string{}
	for k, v := range base {
		vars[k] = v
	}
	for k, v := range o.Vars {
		vars[k] = v
	}
	vars["$scenarioStart"] = start.UTC().Format(time.RFC3339)
	r := newRenderer(vars)
	for k, v := range sc.Variables {
		if _, overridden := o.Vars[k]; !overridden {
			vars[k] = r.str(v)
		}
	}
	run := &scenarioRun{e: e, app: app, sc: sc, vars: vars, shared: shared, kafka: executor.NewKafkaSession(), started: start}
	run.markKafka(ctx)

	halted := ""
	for i := range sc.Tests {
		t := &sc.Tests[i]
		kind := t.Kind
		if kind == "" {
			kind = model.KindPositive
		}
		baseSpec := spec{id: t.ID, name: t.Name, typ: t.Type, kind: kind, authID: run.authFor(t.Auth), mtlsID: firstSet(t.MTLS, sc.MTLS), props: t.Properties,
			expect: t.Expect, retry: t.Retry, capture: t.Capture}
		blk, err := toMap(stepBlock(t))
		if err != nil {
			blk = map[string]any{}
		}
		baseSpec.block = blk

		skipped := func(s spec, why string) model.TestResult {
			return model.TestResult{ID: s.id, Name: s.name, VariantOf: s.variantOf, Type: s.typ, Kind: s.kind, Properties: s.props, Status: model.StatusSkipped, Error: why}
		}
		baseRan := false
		switch {
		case t.Skip:
			emit(skipped(baseSpec, "marked skip"))
		case halted != "":
			emit(skipped(baseSpec, "earlier step "+halted+" failed"))
		case o.Kind == model.KindPositive && kind == model.KindNegative:
			emit(skipped(baseSpec, "negative cases excluded from this run"))
		default:
			tr := run.runOne(ctx, baseSpec)
			baseRan = true
			if tr.Status == model.StatusPassed {
				for k, v := range tr.Captured {
					vars[k] = v
				}
			} else if !t.ContinueOnFail {
				halted = t.ID
			}
			emit(tr)
		}
		for j := range t.Variants {
			v := &t.Variants[j]
			vk := v.Kind
			if vk == "" {
				vk = model.KindNegative
			}
			authID := baseSpec.authID
			if v.Auth != "" {
				authID = run.authFor(v.Auth)
			}
			props := map[string]string{}
			for k, val := range t.Properties {
				props[k] = val
			}
			for k, val := range v.Properties {
				props[k] = val
			}
			vs := spec{id: t.ID + "." + v.ID, name: v.Name, variantOf: t.ID, typ: t.Type, kind: vk, authID: authID, mtlsID: firstSet(v.MTLS, baseSpec.mtlsID), props: props,
				block: merge(blk, variantOverride(t.Type, v)), expect: v.Expect, retry: t.Retry}
			if vs.name == "" {
				vs.name = v.ID
			}
			switch {
			case v.Skip || t.Skip:
				emit(skipped(vs, "marked skip"))
			case !baseRan:
				emit(skipped(vs, "parent test did not run"))
			case o.Kind != "" && o.Kind != vk:
				emit(skipped(vs, vk+" cases excluded from this run"))
			default:
				emit(run.runOne(ctx, vs))
			}
		}
		if o.Until != "" && t.ID == o.Until {
			break
		}
	}
	res.DurationMs = time.Since(start).Milliseconds()
	return res
}

func stepBlock(t *model.Test) any {
	switch t.Type {
	case model.TypeHTTP:
		return t.HTTP
	case model.TypeGRPC:
		return t.GRPC
	case model.TypeKafka:
		return t.Kafka
	case model.TypeMongo:
		return t.Mongo
	case model.TypePostgres:
		return t.Postgres
	case model.TypeLogs:
		return t.Logs
	}
	return nil
}

func variantOverride(typ string, v *model.Variant) map[string]any {
	switch typ {
	case model.TypeHTTP:
		return v.HTTP
	case model.TypeGRPC:
		return v.GRPC
	case model.TypeKafka:
		return v.Kafka
	case model.TypeMongo:
		return v.Mongo
	case model.TypePostgres:
		return v.Postgres
	case model.TypeLogs:
		return v.Logs
	}
	return nil
}

// authFor applies the fallback chain test -> scenario -> application.
func (r *scenarioRun) authFor(id string) string {
	if id == "" {
		id = r.sc.Auth
	}
	if id == "" {
		id = r.app.DefaultAuth
	}
	if id == "" {
		id = model.AuthNone
	}
	return id
}

func firstSet(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// owner splits "other-app/id" and returns the variables to render that
// application's assets with: its own environment values take precedence.
func (r *scenarioRun) owner(id string) (appID, localID string, vars map[string]string) {
	appID, localID, vars = r.app.ID, id, r.vars
	if o, rest, ok := strings.Cut(id, "/"); ok {
		appID, localID = o, rest
		if appID != r.app.ID {
			vars = copyVars(r.vars)
			for k, v := range r.shared.appEnv[appID] {
				vars[k] = v
			}
		}
	}
	return
}

// credentials combines an auth profile with a certificate set.
//
// authID is a profile id, "other-app/profile" or "none". mtlsID is a
// certificate set id, "other-app/set", "none" (present no certificate even
// if the profile has one) or "" (use whatever the profile carries). Both
// are read as they apply in the environment being run.
func (r *scenarioRun) credentials(ctx context.Context, authID, mtlsID string) (*auth.Credentials, error) {
	profile := &model.AuthProfile{ID: model.AuthNone, Type: model.AuthNone}
	certsDir := r.e.Store.AssetDir(r.app.ID, "certs")
	if authID != model.AuthNone && authID != "" {
		appID, id, vars := r.owner(authID)
		stored, err := r.e.Store.GetAuth(appID, id)
		if err != nil {
			return nil, fmt.Errorf("auth profile %q: %w", authID, err)
		}
		eff, err := stored.ForEnv(r.shared.env)
		if err != nil {
			return nil, fmt.Errorf("auth profile %q: %w", authID, err)
		}
		m, err := toMap(eff)
		if err != nil {
			return nil, err
		}
		rd := newRenderer(vars)
		rendered := rd.value(m)
		if err := rd.err(); err != nil {
			return nil, fmt.Errorf("auth profile %q: %w", authID, err)
		}
		profile = &model.AuthProfile{}
		if err := fromMap(rendered, profile); err != nil {
			return nil, err
		}
		profile.ID = id
		certsDir = r.e.Store.AssetDir(appID, "certs")
	}
	switch mtlsID {
	case "":
	case model.AuthNone:
		profile.TLS = nil
		if profile.Type == model.AuthMTLS {
			profile.Type = model.AuthNone
		}
	default:
		appID, id, vars := r.owner(mtlsID)
		set, err := r.e.Store.GetCertSet(appID, id)
		if err != nil {
			return nil, fmt.Errorf("certificate set %q: %w", mtlsID, err)
		}
		t := set.For(r.shared.env)
		rd := newRenderer(vars)
		t.ServerName = rd.str(t.ServerName)
		if err := rd.err(); err != nil {
			return nil, fmt.Errorf("certificate set %q: %w", mtlsID, err)
		}
		if owner := r.e.Store.AssetDir(appID, "certs"); owner != certsDir && profile.TLS != nil {
			// Files of the profile and of the set live in different folders;
			// the set wins, so the profile's own files are not needed.
			profile.TLS = nil
		}
		profile.TLS = t
		certsDir = r.e.Store.AssetDir(appID, "certs")
	}
	return r.shared.resolver.ResolveIn(ctx, profile, certsDir, authID+"|"+mtlsID)
}

// markKafka notes the end of every topic a consume step will read, before
// the first test runs. Failures are left for the step itself to report.
func (r *scenarioRun) markKafka(ctx context.Context) {
	for i := range r.sc.Tests {
		t := &r.sc.Tests[i]
		if t.Type != model.TypeKafka || t.Kafka == nil || t.Skip || strings.EqualFold(t.Kafka.Action, "produce") {
			continue
		}
		if f := strings.ToLower(t.Kafka.From); f != "" && f != "scenario_start" {
			continue
		}
		m, err := toMap(t.Kafka)
		if err != nil {
			continue
		}
		rd := newRenderer(r.vars)
		rendered := rd.value(m)
		if rd.err() != nil {
			continue
		}
		var step model.KafkaStep
		if fromMap(rendered, &step) != nil {
			continue
		}
		creds, err := r.credentials(ctx, r.authFor(t.Auth), firstSet(t.MTLS, r.sc.MTLS))
		if err != nil {
			continue
		}
		_ = r.kafka.Mark(ctx, &step, creds)
	}
}

func (r *scenarioRun) runOne(ctx context.Context, s spec) model.TestResult {
	start := time.Now()
	tr := model.TestResult{ID: s.id, Name: s.name, VariantOf: s.variantOf, Type: s.typ, Kind: s.kind, Properties: s.props}
	if s.authID != model.AuthNone {
		tr.Auth = s.authID
	}
	tr.MTLS = s.mtlsID
	fail := func(err error) model.TestResult {
		tr.Status, tr.Error = model.StatusError, err.Error()
		tr.DurationMs = time.Since(start).Milliseconds()
		return tr
	}
	rd := newRenderer(r.vars)
	block, _ := rd.value(s.block).(map[string]any)
	var expect model.Expect
	if s.expect != nil {
		em, err := toMap(s.expect)
		if err != nil {
			return fail(err)
		}
		if err := fromMap(rd.value(em), &expect); err != nil {
			return fail(fmt.Errorf("expect: %w", err))
		}
	}
	if err := rd.err(); err != nil {
		return fail(err)
	}
	tr.Request = redact(block)

	// No explicit choice: use the certificate set registered for this host.
	mtlsID := s.mtlsID
	if mtlsID == "" {
		if id := r.matchSet(targetHost(s.typ, block)); id != "" {
			mtlsID = id
		}
	}
	tr.MTLS = mtlsID

	exec, err := r.prepare(s.typ, block)
	if err != nil {
		return fail(err)
	}
	attempts, delay := 1, 500*time.Millisecond
	if s.retry != nil && s.retry.Attempts > 1 {
		attempts = s.retry.Attempts
		if s.retry.DelayMs > 0 {
			delay = time.Duration(s.retry.DelayMs) * time.Millisecond
		}
	}
	for n := 1; n <= attempts; n++ {
		tr.Attempts = n
		tr.Error, tr.Assertions, tr.Response = "", nil, nil
		creds, err := r.credentials(ctx, s.authID, mtlsID)
		var resp *model.Response
		if err == nil {
			resp, err = exec(ctx, creds)
		}
		tr.Assertions, tr.Error = evaluate(s, &expect, resp, err)
		if resp != nil {
			cp := *resp
			if len(cp.Body) > maxStoredBody {
				cp.Body = cp.Body[:maxStoredBody] + "\n… truncated"
			}
			tr.Response = &cp
		}
		tr.Status = model.StatusPassed
		if tr.Error != "" {
			tr.Status = model.StatusError
		}
		for _, a := range tr.Assertions {
			if !a.Passed && tr.Status == model.StatusPassed {
				tr.Status = model.StatusFailed
			}
		}
		if tr.Status == model.StatusPassed && resp != nil && len(s.capture) > 0 {
			tr.Captured = map[string]string{}
			for name, path := range s.capture {
				val, ok := captureValue(resp, path)
				if !ok {
					tr.Status = model.StatusFailed
					tr.Assertions = append(tr.Assertions, model.AssertionResult{Check: "capture " + name + " from " + path, Expected: "a value", Actual: "<missing>"})
					continue
				}
				tr.Captured[name] = val
			}
		}
		if tr.Status == model.StatusPassed || n == attempts || ctx.Err() != nil {
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(delay):
		}
	}
	tr.DurationMs = time.Since(start).Milliseconds()
	return tr
}

// redact hides credential fields before a request is stored with a run.
func redact(block map[string]any) map[string]any {
	out := copyAny(block)
	for _, k := range []string{"apiKey", "appKey"} {
		if v, ok := out[k].(string); ok && v != "" {
			out[k] = model.SecretMask
		}
	}
	return out
}

func copyAny(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

type execFn func(ctx context.Context, creds *auth.Credentials) (*model.Response, error)

func (r *scenarioRun) prepare(typ string, block map[string]any) (execFn, error) {
	switch typ {
	case model.TypeHTTP:
		var s model.HTTPStep
		if err := fromMap(block, &s); err != nil {
			return nil, fmt.Errorf("http: %w", err)
		}
		return func(ctx context.Context, c *auth.Credentials) (*model.Response, error) {
			return executor.HTTP(ctx, &s, c)
		}, nil
	case model.TypeGRPC:
		var s model.GRPCStep
		if err := fromMap(block, &s); err != nil {
			return nil, fmt.Errorf("grpc: %w", err)
		}
		dir := r.e.Store.AssetDir(r.app.ID, "protos")
		return func(ctx context.Context, c *auth.Credentials) (*model.Response, error) {
			return executor.GRPC(ctx, &s, c, dir)
		}, nil
	case model.TypeKafka:
		var s model.KafkaStep
		if err := fromMap(block, &s); err != nil {
			return nil, fmt.Errorf("kafka: %w", err)
		}
		return func(ctx context.Context, c *auth.Credentials) (*model.Response, error) {
			return executor.Kafka(ctx, &s, c, r.kafka)
		}, nil
	case model.TypeMongo:
		var s model.MongoStep
		if err := fromMap(block, &s); err != nil {
			return nil, fmt.Errorf("mongo: %w", err)
		}
		return func(ctx context.Context, c *auth.Credentials) (*model.Response, error) {
			return executor.Mongo(ctx, &s, c)
		}, nil
	case model.TypePostgres:
		var s model.PostgresStep
		if err := fromMap(block, &s); err != nil {
			return nil, fmt.Errorf("postgres: %w", err)
		}
		return func(ctx context.Context, c *auth.Credentials) (*model.Response, error) {
			return executor.Postgres(ctx, &s, c)
		}, nil
	case model.TypeLogs:
		var s model.LogsStep
		if err := fromMap(block, &s); err != nil {
			return nil, fmt.Errorf("logs: %w", err)
		}
		return func(ctx context.Context, c *auth.Credentials) (*model.Response, error) {
			return executor.Logs(ctx, &s, c, r.started)
		}, nil
	}
	return nil, fmt.Errorf("unknown test type %q", typ)
}

var readQuery = regexp.MustCompile(`(?i)^\s*(select|with|show|values|table)\b`)

// evaluate turns a response (or a transport error) into assertion results.
// The returned string is non-empty when the step could not be judged at all.
func evaluate(s spec, x *model.Expect, resp *model.Response, err error) ([]model.AssertionResult, string) {
	var out []model.AssertionResult
	add := func(check string, passed bool, expected, actual string) {
		out = append(out, model.AssertionResult{Check: check, Passed: passed, Expected: expected, Actual: actual})
	}
	if x.Error != "" {
		if err == nil {
			add("call fails with error", false, x.Error, "call succeeded: "+resp.StatusText)
		} else if x.Error == "*" {
			// Any failure will do: useful where the exact message depends on
			// timing, such as a server dropping a connection without a certificate.
			add("call fails", true, "any error", err.Error())
		} else {
			add("call fails with error", strings.Contains(strings.ToLower(err.Error()), strings.ToLower(x.Error)), x.Error, err.Error())
		}
		return out, ""
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, "timed out: " + err.Error()
		}
		return nil, err.Error()
	}

	positive := s.kind != model.KindNegative
	primary := x.Status != nil || len(x.StatusIn) > 0 || x.Code != "" || x.Count != nil || x.Found != nil
	switch s.typ {
	case model.TypeHTTP:
		got := strconv.Itoa(resp.Status)
		ok2xx := resp.Status >= 200 && resp.Status <= 299
		if x.Status != nil {
			add("status", resp.Status == *x.Status, strconv.Itoa(*x.Status), got)
		}
		if len(x.StatusIn) > 0 {
			in := false
			for _, c := range x.StatusIn {
				in = in || c == resp.Status
			}
			add("status in", in, fmt.Sprint(x.StatusIn), got)
		}
		if !primary && positive {
			add("status is 2xx (default for a positive test)", ok2xx, "2xx", got)
		} else if !primary {
			add("status is not 2xx (default for a negative test)", !ok2xx, "not 2xx", got)
		}
		for name, want := range x.Headers {
			have := http.Header(resp.Headers).Get(name)
			add("header "+name, have == want, want, have)
		}
	case model.TypeGRPC:
		if x.Code != "" {
			add("grpc code", strings.EqualFold(normCode(x.Code), normCode(resp.StatusText)), strings.ToUpper(x.Code), resp.StatusText)
		} else if positive {
			add("grpc code is OK (default for a positive test)", resp.Status == 0, "OK", resp.StatusText)
		} else {
			add("grpc code is not OK (default for a negative test)", resp.Status != 0, "not OK", resp.StatusText)
		}
	case model.TypeKafka:
		if act, _ := s.block["action"].(string); !strings.EqualFold(act, "produce") {
			found := gjson.Get(resp.JSON, "found").Bool()
			want := positive
			label := "message found (default for a positive test)"
			if !positive {
				label = "no matching message (default for a negative test)"
			}
			if x.Found != nil {
				want, label = *x.Found, "message found"
			}
			add(label, found == want, strconv.FormatBool(want), strconv.FormatBool(found))
		}
	case model.TypeMongo, model.TypePostgres, model.TypeLogs:
		count := int(gjson.Get(resp.JSON, "count").Int())
		noun := "documents"
		isRead := true
		if s.typ == model.TypeLogs {
			noun = "log entries"
		}
		if s.typ == model.TypePostgres {
			noun = "rows"
			q, _ := s.block["query"].(string)
			isRead = readQuery.MatchString(q)
		}
		switch {
		case x.Count != nil:
			add(noun+" returned", count == *x.Count, strconv.Itoa(*x.Count), strconv.Itoa(count))
		case !isRead:
			add("statement executed", true, "", "")
		case positive:
			add("at least one of the "+noun+" exists (default for a positive test)", count > 0, ">= 1", strconv.Itoa(count))
		default:
			add("no "+noun+" exist (default for a negative test)", count == 0, "0", strconv.Itoa(count))
		}
	}
	for _, a := range x.Body {
		out = append(out, assert.Eval(resp.JSON, a))
	}
	for _, sub := range x.BodyContains {
		add("body contains", strings.Contains(resp.Body, sub), sub, snippet(resp.Body))
	}
	if x.MaxDurationMs > 0 {
		add("duration", resp.DurationMs <= int64(x.MaxDurationMs), fmt.Sprintf("<= %d ms", x.MaxDurationMs), fmt.Sprintf("%d ms", resp.DurationMs))
	}
	return out, ""
}

func normCode(s string) string {
	return strings.ToUpper(strings.NewReplacer("_", "", " ", "", "CANCELLED", "CANCELED").Replace(strings.ToUpper(s)))
}

func snippet(s string) string {
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}

// captureValue reads "$status", "$header.<Name>" or a JSON path.
func captureValue(resp *model.Response, path string) (string, bool) {
	if path == "$status" {
		return strconv.Itoa(resp.Status), true
	}
	if strings.HasPrefix(path, "$header.") {
		v := http.Header(resp.Headers).Get(strings.TrimPrefix(path, "$header."))
		return v, v != ""
	}
	g := gjson.Get(resp.JSON, path)
	if !g.Exists() || g.Type == gjson.Null {
		return "", false
	}
	return g.String(), true
}
