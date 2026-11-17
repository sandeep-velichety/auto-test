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
func (s *Store) ImportApp(data []byte) (string, error) {
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
