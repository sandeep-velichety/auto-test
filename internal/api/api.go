// Package api exposes the workspace and the runner over HTTP for the UI.
package api

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/testplane/testplane/internal/assert"
	"github.com/testplane/testplane/internal/auth"
	"github.com/testplane/testplane/internal/engine"
	"github.com/testplane/testplane/internal/model"
	"github.com/testplane/testplane/internal/store"
)

type Server struct {
	Store  *store.Store
	Engine *engine.Engine
	UI     fs.FS // built front end; nil serves the API only

	// Token, when set, must accompany every API call as
	// "Authorization: Bearer <token>" (except /api/meta and preflights).
	Token string
	// AllowOrigins lists web origins other than the server's own that may
	// call the API from a browser. "*" allows any. When empty, any origin is
	// allowed if a token is set (requests still need the token) and none
	// otherwise.
	AllowOrigins []string
	// RetentionDays, when positive, deletes stored runs older than this.
	RetentionDays int
}

func New(s *store.Store, ui fs.FS) *Server {
	return &Server{Store: s, Engine: engine.New(s), UI: ui}
}

type authView struct {
	model.AuthProfile
	CertChecks []auth.Check `json:"certChecks,omitempty"`
}

type certSetView struct {
	model.CertSet
	// Checks describe the files used in the environment asked for.
	Checks []auth.Check `json:"checks"`
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	switch {
	case errors.Is(err, store.ErrNotFound):
		code = http.StatusNotFound
	case errors.Is(err, store.ErrInvalid):
		code = http.StatusBadRequest
	}
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func badRequest(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 5<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%w: request body: %v", store.ErrInvalid, err)
	}
	return nil
}

func (s *Server) originAllowed(origin string) bool {
	if len(s.AllowOrigins) == 0 {
		return s.Token != ""
	}
	for _, o := range s.AllowOrigins {
		if o == "*" || strings.EqualFold(strings.TrimRight(o, "/"), origin) {
			return true
		}
	}
	return false
}

// guard adds CORS headers and enforces the access token on API routes.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && s.originAllowed(origin) {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Add("Vary", "Origin")
			h.Set("Access-Control-Allow-Methods", "GET, PUT, POST, DELETE, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Testplane-User")
			h.Set("Access-Control-Max-Age", "600")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if s.Token != "" && r.URL.Path != "/api/meta" {
			got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !ok || subtle.ConstantTimeCompare([]byte(got), []byte(s.Token)) != 1 {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "this backend needs its access token"})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// Prune applies the retention setting once.
func (s *Server) Prune() {
	if s.RetentionDays > 0 {
		_, _ = s.Store.PruneRuns(time.Now().AddDate(0, 0, -s.RetentionDays))
	}
}

// Handler builds the routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	st := s.Store

	// Open on purpose: it is the health check and tells a client whether a
	// token is needed. It reveals nothing about the workspace.
	mux.HandleFunc("GET /api/meta", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"name": "testplane", "authRequired": s.Token != "", "testTypes": model.TestTypes, "authTypes": model.AuthTypes, "assertionOps": assert.Ops})
	})

	// applications
	mux.HandleFunc("GET /api/apps", func(w http.ResponseWriter, r *http.Request) {
		apps, err := st.ListApps()
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, 200, apps)
	})
	mux.HandleFunc("GET /api/apps/{app}", func(w http.ResponseWriter, r *http.Request) {
		a, err := st.GetApp(r.PathValue("app"))
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, 200, a)
	})
	mux.HandleFunc("PUT /api/apps/{app}", func(w http.ResponseWriter, r *http.Request) {
		var a model.Application
		if err := decode(r, &a); err != nil {
			fail(w, err)
			return
		}
		a.ID = r.PathValue("app")
		if err := st.SaveApp(&a); err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, 200, a)
	})
	mux.HandleFunc("DELETE /api/apps/{app}", func(w http.ResponseWriter, r *http.Request) {
		if err := st.DeleteApp(r.PathValue("app")); err != nil {
			fail(w, err)
			return
		}
		w.WriteHeader(204)
	})
	mux.HandleFunc("GET /api/apps/{app}/export", func(w http.ResponseWriter, r *http.Request) {
		app := r.PathValue("app")
		var buf bytes.Buffer
		// Keys and literal secrets never leave through the API.
		if err := st.ExportApp(app, &buf, false); err != nil {
			fail(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+app+`.zip"`)
		_, _ = w.Write(buf.Bytes())
	})
	mux.HandleFunc("POST /api/import", func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(io.LimitReader(r.Body, 50<<20))
		if err != nil {
			fail(w, err)
			return
		}
		id, err := st.ImportApp(b)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, 200, map[string]string{"id": id})
	})

	// scenarios
	mux.HandleFunc("GET /api/apps/{app}/scenarios", func(w http.ResponseWriter, r *http.Request) {
		list, err := st.ListScenarios(r.PathValue("app"))
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, 200, list)
	})
	mux.HandleFunc("GET /api/apps/{app}/scenarios/{id}", func(w http.ResponseWriter, r *http.Request) {
		sc, err := st.GetScenario(r.PathValue("app"), r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, 200, sc)
	})
	mux.HandleFunc("PUT /api/apps/{app}/scenarios/{id}", func(w http.ResponseWriter, r *http.Request) {
		var sc model.Scenario
		if err := decode(r, &sc); err != nil {
			fail(w, err)
			return
		}
		sc.ID = r.PathValue("id")
		if err := st.SaveScenario(r.PathValue("app"), &sc); err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, 200, sc)
	})
	mux.HandleFunc("DELETE /api/apps/{app}/scenarios/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := st.DeleteScenario(r.PathValue("app"), r.PathValue("id")); err != nil {
			fail(w, err)
			return
		}
		w.WriteHeader(204)
	})
	// The whole scenario in one file: the scenario plus the auth profiles
	// and certificate sets it uses.
	mux.HandleFunc("GET /api/apps/{app}/scenarios/{id}/export", func(w http.ResponseWriter, r *http.Request) {
		b, err := st.ExportScenario(r.PathValue("app"), r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+r.PathValue("id")+`.scenario.yaml"`)
		_, _ = w.Write(b)
	})
	mux.HandleFunc("POST /api/apps/{app}/scenarios/import", func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(io.LimitReader(r.Body, 5<<20))
		if err != nil {
			fail(w, err)
			return
		}
		res, err := st.ImportScenario(r.PathValue("app"), b, r.URL.Query().Get("overwrite") == "true")
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, 200, res)
	})
	// Lists the services and methods of a gRPC target for the method picker.
	mux.HandleFunc("POST /api/apps/{app}/grpc/describe", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Environment string   `json:"environment"`
			Auth        string   `json:"auth"`
			MTLS        string   `json:"mtls"`
			Target      string   `json:"target"`
			Plaintext   bool     `json:"plaintext"`
			ProtoFiles  []string `json:"protoFiles"`
		}
		if err := decode(r, &req); err != nil {
			fail(w, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
		defer cancel()
		out, err := s.Engine.DescribeGRPC(ctx, r.PathValue("app"), req.Environment, req.Auth, req.MTLS, model.GRPCStep{Target: req.Target, Plaintext: req.Plaintext, ProtoFiles: req.ProtoFiles})
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, 200, out)
	})
	mux.HandleFunc("GET /api/apps/{app}/scenarios/{id}/yaml", func(w http.ResponseWriter, r *http.Request) {
		b, err := st.GetScenarioYAML(r.PathValue("app"), r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
		_, _ = w.Write(b)
	})
	mux.HandleFunc("PUT /api/apps/{app}/scenarios/{id}/yaml", func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(io.LimitReader(r.Body, 5<<20))
		if err != nil {
			fail(w, err)
			return
		}
		sc, err := st.SaveScenarioYAML(r.PathValue("app"), r.PathValue("id"), b)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, 200, sc)
	})

	// auth profiles
	// envOf returns the environment a request is about (?env=, else the
	// application's default).
	envOf := func(r *http.Request) string {
		if e := r.URL.Query().Get("env"); e != "" {
			return e
		}
		if a, err := st.GetApp(r.PathValue("app")); err == nil {
			return a.DefaultEnvironment
		}
		return ""
	}
	view := func(app, env string, p model.AuthProfile) authView {
		eff, err := p.ForEnv(env)
		if err != nil {
			eff = &p
		}
		// Stored secrets are write-only: they go out masked.
		return authView{AuthProfile: p.Masked(), CertChecks: auth.CertChecks(eff, st.AssetDir(app, "certs"))}
	}
	setView := func(app, env string, c model.CertSet) certSetView {
		return certSetView{CertSet: c, Checks: auth.TLSChecks(c.For(env), false, st.AssetDir(app, "certs"))}
	}

	// certificate sets
	mux.HandleFunc("GET /api/apps/{app}/tls", func(w http.ResponseWriter, r *http.Request) {
		app := r.PathValue("app")
		list, err := st.ListCertSets(app)
		if err != nil {
			fail(w, err)
			return
		}
		env := envOf(r)
		out := make([]certSetView, 0, len(list))
		for _, c := range list {
			out = append(out, setView(app, env, c))
		}
		writeJSON(w, 200, out)
	})
	mux.HandleFunc("PUT /api/apps/{app}/tls/{id}", func(w http.ResponseWriter, r *http.Request) {
		var c model.CertSet
		if err := decode(r, &c); err != nil {
			fail(w, err)
			return
		}
		c.ID = r.PathValue("id")
		if err := st.SaveCertSet(r.PathValue("app"), &c); err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, 200, setView(r.PathValue("app"), envOf(r), c))
	})
	mux.HandleFunc("DELETE /api/apps/{app}/tls/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := st.DeleteCertSet(r.PathValue("app"), r.PathValue("id")); err != nil {
			fail(w, err)
			return
		}
		w.WriteHeader(204)
	})

	mux.HandleFunc("GET /api/apps/{app}/auth", func(w http.ResponseWriter, r *http.Request) {
		app := r.PathValue("app")
		list, err := st.ListAuth(app)
		if err != nil {
			fail(w, err)
			return
		}
		env := envOf(r)
		out := make([]authView, 0, len(list))
		for _, p := range list {
			out = append(out, view(app, env, p))
		}
		writeJSON(w, 200, out)
	})
	mux.HandleFunc("PUT /api/apps/{app}/auth/{id}", func(w http.ResponseWriter, r *http.Request) {
		var p model.AuthProfile
		if err := decode(r, &p); err != nil {
			fail(w, err)
			return
		}
		p.ID = r.PathValue("id")
		// A masked value coming back means "leave the stored secret alone".
		old, _ := st.GetAuth(r.PathValue("app"), p.ID)
		p.KeepSecrets(old)
		if err := st.SaveAuth(r.PathValue("app"), &p); err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, 200, view(r.PathValue("app"), envOf(r), p))
	})
	mux.HandleFunc("DELETE /api/apps/{app}/auth/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := st.DeleteAuth(r.PathValue("app"), r.PathValue("id")); err != nil {
			fail(w, err)
			return
		}
		w.WriteHeader(204)
	})
	// Try a profile for real: load its certificates and fetch its token.
	mux.HandleFunc("POST /api/apps/{app}/auth/{id}/check", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
		defer cancel()
		res, err := s.Engine.CheckAuth(ctx, r.PathValue("app"), r.PathValue("id"), r.URL.Query().Get("env"), r.URL.Query().Get("mtls"))
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, 200, res)
	})

	// asset files
	mux.HandleFunc("GET /api/apps/{app}/assets", func(w http.ResponseWriter, r *http.Request) {
		list, err := st.ListAssets(r.PathValue("app"))
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, 200, list)
	})
	mux.HandleFunc("PUT /api/apps/{app}/assets/{category}/{name}", func(w http.ResponseWriter, r *http.Request) {
		if err := st.SaveAsset(r.PathValue("app"), r.PathValue("category"), r.PathValue("name"), r.Body); err != nil {
			fail(w, err)
			return
		}
		w.WriteHeader(204)
	})
	mux.HandleFunc("DELETE /api/apps/{app}/assets/{category}/{name}", func(w http.ResponseWriter, r *http.Request) {
		if err := st.DeleteAsset(r.PathValue("app"), r.PathValue("category"), r.PathValue("name")); err != nil {
			fail(w, err)
			return
		}
		w.WriteHeader(204)
	})

	// runs
	mux.HandleFunc("POST /api/apps/{app}/run", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Environment string            `json:"environment"`
			Scenarios   []string          `json:"scenarios"`
			Tags        []string          `json:"tags"`
			Kind        string            `json:"kind"`
			Until       string            `json:"until"`
			Vars        map[string]string `json:"vars"`
		}
		if r.ContentLength != 0 {
			if err := decode(r, &req); err != nil {
				fail(w, err)
				return
			}
		}
		if req.Kind != "" && req.Kind != model.KindPositive && req.Kind != model.KindNegative {
			badRequest(w, "kind must be positive, negative or empty")
			return
		}
		res, err := s.Engine.Run(r.Context(), engine.Options{App: r.PathValue("app"), Env: req.Environment, Scenarios: req.Scenarios, Tags: req.Tags, Kind: req.Kind, Until: req.Until, Vars: req.Vars,
			By: clip(r.Header.Get("X-Testplane-User"), 80), Source: "server"})
		if err != nil {
			fail(w, err)
			return
		}
		// A partial run made while authoring ("run to this step") is not history.
		if req.Until == "" {
			if err := st.SaveRun(res); err != nil {
				fail(w, err)
				return
			}
			s.Prune()
		}
		writeJSON(w, 200, res)
	})
	// History. Filters: scenario, status, env, from, to (RFC 3339 or
	// YYYY-MM-DD), limit.
	mux.HandleFunc("GET /api/apps/{app}/runs", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		f := store.RunFilter{Scenario: q.Get("scenario"), Status: q.Get("status"), Environment: q.Get("env"), Limit: 100}
		if n, err := strconv.Atoi(q.Get("limit")); err == nil && n > 0 {
			f.Limit = min(n, 1000)
		}
		var err error
		if f.From, err = parseTime(q.Get("from"), false); err != nil {
			badRequest(w, "from: "+err.Error())
			return
		}
		if f.To, err = parseTime(q.Get("to"), true); err != nil {
			badRequest(w, "to: "+err.Error())
			return
		}
		list, err := st.ListRuns(r.PathValue("app"), f)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, 200, list)
	})
	// Lets a pipeline store the result of a headless run here
	// (testplane run --publish URL).
	mux.HandleFunc("POST /api/apps/{app}/runs", func(w http.ResponseWriter, r *http.Request) {
		var res model.RunResult
		if err := json.NewDecoder(io.LimitReader(r.Body, 50<<20)).Decode(&res); err != nil {
			badRequest(w, "request body: "+err.Error())
			return
		}
		if res.Application != r.PathValue("app") {
			badRequest(w, "run belongs to application "+res.Application)
			return
		}
		if _, err := st.GetApp(res.Application); err != nil {
			fail(w, err)
			return
		}
		if res.StartedAt.IsZero() || res.Scenarios == nil {
			badRequest(w, "not a run result")
			return
		}
		if err := st.SaveRun(&res); err != nil {
			fail(w, err)
			return
		}
		s.Prune()
		writeJSON(w, 201, map[string]string{"id": res.ID})
	})
	mux.HandleFunc("GET /api/apps/{app}/runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		res, err := st.GetRun(r.PathValue("app"), r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, 200, res)
	})

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such endpoint"})
	})

	if s.UI != nil {
		files := http.FileServer(http.FS(s.UI))
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			// Single-page app: unknown paths fall back to index.html.
			p := strings.TrimPrefix(r.URL.Path, "/")
			if p != "" {
				if _, err := fs.Stat(s.UI, p); err != nil {
					r.URL.Path = "/"
				}
			}
			files.ServeHTTP(w, r)
		})
	}
	return s.guard(mux)
}

func clip(v string, n int) string {
	v = strings.TrimSpace(v)
	if len(v) > n {
		v = v[:n]
	}
	return v
}

// parseTime accepts RFC 3339 or a plain date. A plain date used as an upper
// bound covers that whole day.
func parseTime(v string, endOfDay bool) (time.Time, error) {
	if v == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	t, err := time.Parse("2006-01-02", v)
	if err != nil {
		return time.Time{}, fmt.Errorf("use YYYY-MM-DD or RFC 3339")
	}
	if endOfDay {
		t = t.Add(24*time.Hour - time.Nanosecond)
	}
	return t, nil
}
