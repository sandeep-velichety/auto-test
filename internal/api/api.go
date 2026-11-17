// Package api exposes the workspace and the runner over HTTP for the UI.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
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
}

func New(s *store.Store, ui fs.FS) *Server {
	return &Server{Store: s, Engine: engine.New(s), UI: ui}
}

type authView struct {
	model.AuthProfile
	CertChecks []auth.Check `json:"certChecks,omitempty"`
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

// Handler builds the routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	st := s.Store

	mux.HandleFunc("GET /api/meta", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"testTypes": model.TestTypes, "authTypes": model.AuthTypes, "assertionOps": assert.Ops, "workspace": st.Root})
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
		if err := st.ExportApp(app, &buf, r.URL.Query().Get("keys") == "true"); err != nil {
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
	view := func(app string, p model.AuthProfile) authView {
		return authView{AuthProfile: p, CertChecks: auth.CertChecks(&p, st.AssetDir(app, "certs"))}
	}
	mux.HandleFunc("GET /api/apps/{app}/auth", func(w http.ResponseWriter, r *http.Request) {
		app := r.PathValue("app")
		list, err := st.ListAuth(app)
		if err != nil {
			fail(w, err)
			return
		}
		out := make([]authView, 0, len(list))
		for _, p := range list {
			out = append(out, view(app, p))
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
		if err := st.SaveAuth(r.PathValue("app"), &p); err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, 200, view(r.PathValue("app"), p))
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
		res, err := s.Engine.CheckAuth(ctx, r.PathValue("app"), r.PathValue("id"), r.URL.Query().Get("env"))
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
		res, err := s.Engine.Run(r.Context(), engine.Options{App: r.PathValue("app"), Env: req.Environment, Scenarios: req.Scenarios, Tags: req.Tags, Kind: req.Kind, Vars: req.Vars})
		if err != nil {
			fail(w, err)
			return
		}
		if err := st.SaveRun(res); err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, 200, res)
	})
	mux.HandleFunc("GET /api/apps/{app}/runs", func(w http.ResponseWriter, r *http.Request) {
		list, err := st.ListRuns(r.PathValue("app"), 50)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, 200, list)
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
	return mux
}
