package store

import (
	"archive/zip"
	"bytes"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/goccy/go-yaml"

	"github.com/testplane/testplane/internal/model"
)

// ExportApp writes an application as a zip: its folder, plus a copy of the
// global auth profiles, certificate sets and certificate files its
// scenarios use (under "global/"), so the zip is complete on its own.
// Unless includeKeys is set, private keys are left out and literal secrets
// in auth profiles are replaced with a placeholder.
func (s *Store) ExportApp(app string, w io.Writer, includeKeys bool) error {
	return s.exportApp(app, w, includeKeys, true)
}

// ExportAppOnly writes just the application: its settings, scenarios and
// proto files, with nothing from global/. This is what is copied to
// another backend, which keeps its own auth and certificates.
func (s *Store) ExportAppOnly(app string, w io.Writer) error {
	return s.exportApp(app, w, false, false)
}

func (s *Store) exportApp(app string, w io.Writer, includeKeys, global bool) error {
	a, err := s.GetApp(app)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(w)
	put := func(rel string, b []byte) error {
		f, err := zw.Create(app + "/" + rel)
		if err != nil {
			return err
		}
		_, err = f.Write(b)
		return err
	}
	root := s.AppDir(app)
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if rel != "application.yaml" && !strings.HasPrefix(rel, "scenarios/") && !strings.HasPrefix(rel, "assets/protos/") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return put(rel, b)
	})
	if err != nil {
		return err
	}

	if !global {
		return zw.Close()
	}

	// What the application refers to in global/.
	auths, sets := map[string]bool{}, map[string]bool{}
	if a.DefaultAuth != "" && a.DefaultAuth != model.AuthNone {
		auths[a.DefaultAuth] = true
	}
	scenarios, err := s.ListScenarios(app)
	if err != nil {
		return err
	}
	for i := range scenarios {
		au, se := scenarioRefs(&scenarios[i])
		for _, id := range au {
			auths[id] = true
		}
		for _, id := range se {
			sets[id] = true
		}
	}
	// Sets that apply by host travel too: a call may rely on them.
	if all, err := s.ListCertSets(); err == nil {
		for _, c := range all {
			if len(c.Hosts) > 0 {
				sets[c.ID] = true
			}
		}
	}
	files := map[string]bool{}
	tlsFiles := func(t *model.TLSSettings) {
		if t == nil {
			return
		}
		for _, f := range []string{t.Cert, t.Key, t.CA} {
			if f != "" {
				files[f] = true
			}
		}
	}
	for _, id := range sortedKeys(auths) {
		path, err := s.authPath(id)
		if err != nil {
			continue
		}
		b, err := os.ReadFile(path)
		if err != nil {
			continue // a dangling reference: nothing to ship
		}
		var prof model.AuthProfile
		if yaml.UnmarshalWithOptions(b, &prof, yaml.Strict()) == nil {
			tlsFiles(prof.TLS)
			for env := range prof.Environments {
				if eff, err := prof.ForEnv(env); err == nil {
					tlsFiles(eff.TLS)
				}
			}
			if !includeKeys {
				if redacted, err := yaml.MarshalWithOptions(prof.Masked(), yaml.Indent(2), yaml.IndentSequence(true)); err == nil {
					b = bytes.ReplaceAll(redacted, []byte(model.SecretMask), []byte("CHANGE_ME"))
				}
			}
		}
		if err := put("global/auth/"+id+".yaml", b); err != nil {
			return err
		}
	}
	for _, id := range sortedKeys(sets) {
		path, err := s.tlsPath(id)
		if err != nil {
			continue
		}
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var c model.CertSet
		if yaml.UnmarshalWithOptions(b, &c, yaml.Strict()) == nil {
			tlsFiles(c.For(""))
			for env := range c.Environments {
				tlsFiles(c.For(env))
			}
		}
		if err := put("global/tls/"+id+".yaml", b); err != nil {
			return err
		}
	}
	for _, name := range sortedKeys(files) {
		if !model.ValidFileName(name) {
			continue
		}
		p := filepath.Join(s.CertsDir(), name)
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		if !includeKeys {
			probe := Asset{Category: "certs", Size: info.Size()}
			describe(&probe, p)
			if probe.Kind == "private-key" {
				continue
			}
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := put("global/certs/"+name, b); err != nil {
			return err
		}
	}
	return zw.Close()
}

// ImportApp unpacks a zip produced by ExportApp and returns the app id.
// The application's own files replace what is there. Global auth profiles,
// certificate sets and certificate files from the zip are added only where
// this workspace has none with that name, so shared credentials are never
// overwritten. Zips from before auth became global (assets/auth,
// assets/tls, assets/certs inside the application) are accepted and their
// contents moved to global/ the same way.
//
// An application that already exists is updated only when replace is set.
// Application names stay unique: a zip whose application has the name of a
// different application here is refused.
func (s *Store) ImportApp(data []byte, replace bool) (string, error) {
	return s.ImportAppFor(data, replace, "")
}

// ImportAppFor is ImportApp on a backend that runs one environment only
// (scope, as in model.EnvCovers). When the application already exists, its
// own values for that environment, and its default environment and auth
// profile, are kept: they describe this backend's surroundings, and a copy
// made elsewhere must not replace them. Variables the copy adds are taken.
func (s *Store) ImportAppFor(data []byte, replace bool, scope string) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", invalid("not a zip file: %v", err)
	}
	app := ""
	type item struct {
		rel string
		f   *zip.File
	}
	var items []item
	yamlID := func(name string) bool {
		return strings.HasSuffix(name, ".yaml") && model.ValidID(strings.TrimSuffix(name, ".yaml"))
	}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		parts := strings.Split(f.Name, "/")
		if len(parts) < 2 || !model.ValidID(parts[0]) {
			return "", invalid("unexpected entry %q", f.Name)
		}
		if app == "" {
			app = parts[0]
		}
		if parts[0] != app {
			return "", invalid("zip holds more than one application")
		}
		rel := parts[1:]
		ok := false
		switch {
		case len(rel) == 1 && rel[0] == "application.yaml":
			ok = true
		case len(rel) == 2 && rel[0] == "scenarios" && yamlID(rel[1]):
			ok = true
		case len(rel) == 3 && (rel[0] == "assets" || rel[0] == "global") && (rel[1] == "auth" || rel[1] == "tls") && yamlID(rel[2]):
			ok = true
		case len(rel) == 3 && rel[0] == "assets" && (rel[1] == "certs" || rel[1] == "protos") && (model.ValidFileName(rel[2]) || rel[2] == ".gitkeep"):
			ok = true
		case len(rel) == 3 && rel[0] == "global" && rel[1] == "certs" && model.ValidFileName(rel[2]):
			ok = true
		}
		if !ok {
			return "", invalid("unexpected entry %q", f.Name)
		}
		items = append(items, item{strings.Join(rel, "/"), f})
	}
	if app == "" {
		return "", invalid("zip is empty")
	}
	// What is being imported, before anything is written.
	var incoming model.Application
	for _, it := range items {
		if it.rel != "application.yaml" {
			continue
		}
		rc, err := it.f.Open()
		if err != nil {
			return "", err
		}
		b, err := io.ReadAll(io.LimitReader(rc, 10<<20))
		rc.Close()
		if err != nil || yaml.UnmarshalWithOptions(b, &incoming, yaml.Strict()) != nil {
			return "", invalid("zip has no valid application.yaml")
		}
	}
	if incoming.Name == "" {
		incoming.Name = app
	}
	existing, _ := s.ListApps()
	var before *model.Application
	for i, o := range existing {
		if o.ID == app {
			before = &existing[i]
		}
		if o.ID == app && !replace {
			return "", &ConflictError{Kind: "application", Name: o.Name, Existing: o.ID, Hint: "import as an update to replace its files with the ones in the zip"}
		}
		if o.ID != app && o.Name == incoming.Name {
			return "", &ConflictError{Kind: "application", Name: o.Name, Existing: o.ID, Hint: "the zip holds a different application (id " + app + ") with the same name; rename one of them first"}
		}
	}
	for _, it := range items {
		rc, err := it.f.Open()
		if err != nil {
			return "", err
		}
		b, err := io.ReadAll(io.LimitReader(rc, 10<<20))
		rc.Close()
		if err != nil {
			return "", err
		}
		if rest, ok := strings.CutPrefix(it.rel, "global/"); ok {
			target := filepath.Join(s.GlobalDir(), filepath.FromSlash(rest))
			if _, err := os.Stat(target); err == nil {
				continue // keep what this workspace already has
			}
			if err := writeFile(target, b, 0o600); err != nil {
				return "", err
			}
			continue
		}
		mode := os.FileMode(0o644)
		if strings.HasPrefix(it.rel, "assets/") {
			mode = 0o600
		}
		if err := writeFile(filepath.Join(s.AppDir(app), filepath.FromSlash(it.rel)), b, mode); err != nil {
			return "", err
		}
	}
	if _, err := s.GetApp(app); err != nil {
		return "", invalid("zip has no valid application.yaml: %v", err)
	}
	if before != nil && scope != "" {
		after, err := s.GetApp(app)
		if err != nil {
			return "", err
		}
		for key, mine := range before.Environments {
			if !scopeKey(scope, key) {
				continue
			}
			if after.Environments == nil {
				after.Environments = map[string]map[string]string{}
			}
			merged := map[string]string{}
			for k, v := range after.Environments[key] {
				merged[k] = v
			}
			for k, v := range mine {
				merged[k] = v
			}
			after.Environments[key] = merged
		}
		if before.DefaultEnvironment != "" {
			after.DefaultEnvironment = before.DefaultEnvironment
		}
		if before.DefaultAuth != "" {
			after.DefaultAuth = before.DefaultAuth
		}
		if err := s.SaveApp(after); err != nil {
			return "", err
		}
	}
	// An older zip carries its auth and certificates inside the application.
	m := &migration{s: s, keep: true, auth: map[string]map[string]string{}, sets: map[string]map[string]string{}}
	if err := m.moveApp(app); err != nil {
		return "", err
	}
	if err := m.rewriteRefs(app); err != nil {
		return "", err
	}
	return app, nil
}

// scopeKey reports whether values stored under key belong to the
// environment a backend is limited to: the key itself, or any layer of it.
func scopeKey(scope, key string) bool {
	if model.EnvCovers(scope, key) {
		return true
	}
	for _, layer := range model.EnvLayers(scope) {
		if layer == key {
			return true
		}
	}
	return false
}

// MissingRefs lists the global auth profiles and certificate sets an
// application's scenarios name that this workspace does not have, as
// "auth profile <id>" and "certificate set <id>".
func (s *Store) MissingRefs(app string) []string {
	a, err := s.GetApp(app)
	if err != nil {
		return nil
	}
	scenarios, _ := s.ListScenarios(app)
	return s.missingRefs(a.DefaultAuth, scenarios)
}

func (s *Store) missingRefs(defaultAuth string, scenarios []model.Scenario) []string {
	auths, sets := map[string]bool{}, map[string]bool{}
	if defaultAuth != "" && defaultAuth != model.AuthNone {
		auths[defaultAuth] = true
	}
	for i := range scenarios {
		au, se := scenarioRefs(&scenarios[i])
		for _, id := range au {
			auths[id] = true
		}
		for _, id := range se {
			sets[id] = true
		}
	}
	out := []string{}
	for _, id := range sortedKeys(auths) {
		if _, err := s.GetAuth(id); err != nil {
			out = append(out, "auth profile "+id)
		}
	}
	for _, id := range sortedKeys(sets) {
		if _, err := s.GetCertSet(id); err != nil {
			out = append(out, "certificate set "+id)
		}
	}
	return out
}
