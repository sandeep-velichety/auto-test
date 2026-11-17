package store

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"

	"github.com/testplane/testplane/internal/model"
)

// Migrate brings an older workspace to the current layout and reports what
// it did, one line per change. It is safe to call on every start: a
// workspace that is already current is left untouched.
//
// Auth profiles, certificate sets and certificate files used to live inside
// each application (assets/auth, assets/tls, assets/certs). They are moved
// to global/, where every application can use them:
//
//   - An id or file name that is free keeps its name.
//   - If the global one is identical, the copy is dropped.
//   - If it differs, the moved one is stored as "<application>-<name>" and
//     everything that referred to it is updated.
//   - Variables the profile took from its application ({{baseUrl}}) become
//     {{<application>.baseUrl}}, so the profile means the same thing no
//     matter which application's test uses it.
//   - References in scenarios ("other-app/profile") become plain ids.
//
// The shared environment list (global/settings.yaml) is written from the
// environments the applications already define.
func (s *Store) Migrate() ([]string, error) {
	apps, err := listIDs(filepath.Join(s.Root, "applications"), "")
	if err != nil {
		return nil, err
	}
	m := &migration{s: s, auth: map[string]map[string]string{}, sets: map[string]map[string]string{}}
	if _, err := os.Stat(s.globalPath()); errors.Is(err, fs.ErrNotExist) && len(apps) > 0 {
		g, err := s.GetGlobal()
		if err != nil {
			return nil, err
		}
		if err := s.SaveGlobal(g); err != nil {
			return nil, err
		}
		m.note("wrote global/settings.yaml: environments %s%s", strings.Join(g.Environments, ", "), regionsNote(g))
	}
	for _, app := range apps {
		if err := m.moveApp(app); err != nil {
			return m.notes, fmt.Errorf("application %s: %w", app, err)
		}
	}
	for _, app := range apps {
		if err := m.rewriteRefs(app); err != nil {
			return m.notes, fmt.Errorf("application %s: %w", app, err)
		}
	}
	return m.notes, nil
}

func regionsNote(g *model.Global) string {
	if len(g.Regions) == 0 {
		return ""
	}
	return "; regions " + strings.Join(g.Regions, ", ")
}

type migration struct {
	s *Store
	// keep: when a global item with the same name already exists and
	// differs, keep it and drop the incoming one (imports), instead of
	// storing the incoming one under a new name (migrating a workspace).
	keep  bool
	notes []string
	// application -> old id -> id it has in global/
	auth, sets map[string]map[string]string
}

func (m *migration) note(f string, a ...any) { m.notes = append(m.notes, fmt.Sprintf(f, a...)) }

// moveApp moves one application's legacy assets to global/.
func (m *migration) moveApp(app string) error {
	s := m.s
	legacy := filepath.Join(s.AppDir(app), "assets")
	m.auth[app], m.sets[app] = map[string]string{}, map[string]string{}

	// Variables the application defines: its profiles may rely on them.
	own := map[string]bool{}
	if a, err := s.GetApp(app); err == nil {
		for _, vals := range a.Environments {
			for k := range vals {
				own[k] = true
			}
		}
	}
	qualify := func(raw []byte) []byte {
		return varRe.ReplaceAllFunc(raw, func(match []byte) []byte {
			name := string(varRe.FindSubmatch(match)[1])
			if own[name] {
				return []byte("{{" + app + "." + name + "}}")
			}
			return match
		})
	}

	// 1. certificate and key files
	renamed := map[string]string{}
	entries, _ := os.ReadDir(filepath.Join(legacy, "certs"))
	for _, e := range entries {
		name := e.Name()
		src := filepath.Join(legacy, "certs", name)
		if e.IsDir() || !model.ValidFileName(name) {
			if name == ".gitkeep" {
				_ = os.Remove(src)
			}
			continue
		}
		b, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		target := name
		if have, err := os.ReadFile(filepath.Join(s.CertsDir(), name)); err == nil {
			switch {
			case bytes.Equal(have, b) || m.keep:
				target = ""
			default:
				target = freeName(app+"-"+name, func(n string) bool {
					_, err := os.Stat(filepath.Join(s.CertsDir(), n))
					return err != nil
				})
				renamed[name] = target
			}
		}
		if target != "" {
			if err := writeFile(filepath.Join(s.CertsDir(), target), b, 0o600); err != nil {
				return err
			}
			m.note("%s: certificate file %s moved to global/certs/%s", app, name, target)
		}
		if err := os.Remove(src); err != nil {
			return err
		}
	}
	rename := func(t *model.TLSSettings) bool {
		changed := false
		for _, f := range []*string{&t.Cert, &t.Key, &t.CA} {
			if n, ok := renamed[*f]; ok {
				*f, changed = n, true
			}
		}
		return changed
	}

	// 2. certificate sets
	ids, _ := listIDs(filepath.Join(legacy, "tls"), ".yaml")
	for _, id := range ids {
		src := filepath.Join(legacy, "tls", id+".yaml")
		raw, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		raw = qualify(raw)
		var c model.CertSet
		if err := yaml.UnmarshalWithOptions(raw, &c, yaml.Strict()); err != nil {
			m.note("%s: certificate set %s does not parse and was left in place: %v", app, id, err)
			continue
		}
		top := model.TLSSettings{Cert: c.Cert, Key: c.Key, CA: c.CA}
		changed := rename(&top)
		c.Cert, c.Key, c.CA = top.Cert, top.Key, top.CA
		for env, o := range c.Environments {
			if rename(&o) {
				c.Environments[env], changed = o, true
			}
		}
		if changed {
			// File names changed: rewrite the document (comments are lost).
			if raw, err = yaml.MarshalWithOptions(&c, yaml.Indent(2), yaml.IndentSequence(true)); err != nil {
				return err
			}
		}
		newID, err := m.place("tls", "certificate set", app, id, raw, 0o644)
		if err != nil {
			return err
		}
		m.sets[app][id] = newID
		if err := os.Remove(src); err != nil {
			return err
		}
	}

	// 3. auth profiles
	ids, _ = listIDs(filepath.Join(legacy, "auth"), ".yaml")
	for _, id := range ids {
		src := filepath.Join(legacy, "auth", id+".yaml")
		raw, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		raw = qualify(raw)
		var p model.AuthProfile
		if err := yaml.UnmarshalWithOptions(raw, &p, yaml.Strict()); err != nil {
			m.note("%s: auth profile %s does not parse and was left in place: %v", app, id, err)
			continue
		}
		changed := p.TLS != nil && rename(p.TLS)
		for _, o := range p.Environments {
			if t, ok := o["tls"].(map[string]any); ok {
				for _, k := range []string{"cert", "key", "ca"} {
					if f, _ := t[k].(string); renamed[f] != "" {
						t[k], changed = renamed[f], true
					}
				}
			}
		}
		if changed {
			if raw, err = yaml.MarshalWithOptions(&p, yaml.Indent(2), yaml.IndentSequence(true)); err != nil {
				return err
			}
		}
		newID, err := m.place("auth", "auth profile", app, id, raw, 0o600)
		if err != nil {
			return err
		}
		m.auth[app][id] = newID
		if err := os.Remove(src); err != nil {
			return err
		}
	}

	// 4. the emptied folders
	for _, d := range []string{"auth", "tls", "certs"} {
		dir := filepath.Join(legacy, d)
		_ = os.Remove(filepath.Join(dir, ".gitkeep"))
		_ = os.Remove(dir) // fails, harmlessly, while something is left in it
	}
	return nil
}

// place stores a moved document under global/<kind>/ and returns the id it
// ended up with.
func (m *migration) place(kind, label, app, id string, raw []byte, mode os.FileMode) (string, error) {
	dir := filepath.Join(m.s.GlobalDir(), kind)
	target := id
	if have, err := os.ReadFile(filepath.Join(dir, id+".yaml")); err == nil {
		if sameYAML(have, raw) {
			return id, nil
		}
		if m.keep {
			m.note("%s: kept the existing global %s %s", app, label, id)
			return id, nil
		}
		target = freeName(app+"-"+id, func(n string) bool {
			if !model.ValidID(n) {
				return false
			}
			_, err := os.Stat(filepath.Join(dir, n+".yaml"))
			return err != nil
		})
		raw = idLine.ReplaceAll(raw, []byte("${1}"+target+"${2}"))
	}
	if err := writeFile(filepath.Join(dir, target+".yaml"), raw, mode); err != nil {
		return "", err
	}
	if target == id {
		m.note("%s: %s %s moved to global", app, label, id)
	} else {
		m.note("%s: %s %s moved to global as %s (a different %s already exists there)", app, label, id, target, id)
	}
	return target, nil
}

var idLine = regexp.MustCompile(`(?m)^(id:[ \t]*)[^\s#]+([ \t]*(?:#.*)?)$`)

// sameYAML reports whether two documents hold the same data, ignoring
// comments and layout.
func sameYAML(a, b []byte) bool {
	var x, y any
	if yaml.Unmarshal(a, &x) != nil || yaml.Unmarshal(b, &y) != nil {
		return bytes.Equal(a, b)
	}
	return reflect.DeepEqual(x, y)
}

// freeName returns base, or base-2, base-3, ... : the first that free accepts.
func freeName(base string, free func(string) bool) string {
	if free(base) {
		return base
	}
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for i := 2; ; i++ {
		if n := fmt.Sprintf("%s-%d%s", stem, i, ext); free(n) {
			return n
		}
	}
}

// newRef returns what a reference written in app's scenarios should read
// now that auth and certificate sets are global.
func newRef(moved map[string]map[string]string, app, ref string) string {
	if ref == "" || ref == model.AuthNone {
		return ref
	}
	owner, id := app, ref
	if o, rest, ok := strings.Cut(ref, "/"); ok {
		owner, id = o, rest
	}
	if n, ok := moved[owner][id]; ok {
		return n
	}
	return id // "other-app/id" from before: the application prefix is gone
}

// rewriteRefs updates the auth and certificate set references of one
// application: its default auth profile and every scenario.
func (m *migration) rewriteRefs(app string) error {
	s := m.s
	a, err := s.GetApp(app)
	if err != nil {
		return nil // not a readable application: nothing to update
	}
	if n := newRef(m.auth, app, a.DefaultAuth); n != a.DefaultAuth {
		path := filepath.Join(s.AppDir(app), "application.yaml")
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out := replaceRef(raw, "defaultAuth", a.DefaultAuth, n)
		var check model.Application
		if yaml.UnmarshalWithOptions(out, &check, yaml.Strict()) == nil && check.DefaultAuth == n {
			err = writeFile(path, out, 0o644)
		} else {
			a.DefaultAuth = n
			err = s.SaveApp(a)
		}
		if err != nil {
			return err
		}
		m.note("%s: default auth profile is now %s", app, n)
	}
	ids, _ := listIDs(filepath.Join(s.AppDir(app), "scenarios"), ".yaml")
	for _, id := range ids {
		sc, err := s.GetScenario(app, id)
		if err != nil {
			continue // broken files are reported elsewhere; leave them alone
		}
		auths, sets := scenarioRefs(sc)
		authChange, setChange := map[string]string{}, map[string]string{}
		for _, ref := range auths {
			if n := newRef(m.auth, app, ref); n != ref {
				authChange[ref] = n
			}
		}
		for _, ref := range sets {
			if n := newRef(m.sets, app, ref); n != ref {
				setChange[ref] = n
			}
		}
		if len(authChange)+len(setChange) == 0 {
			continue
		}
		apply := func(v *string, change map[string]string) {
			if n, ok := change[*v]; ok {
				*v = n
			}
		}
		want := *sc
		want.Tests = append([]model.Test{}, sc.Tests...)
		apply(&want.Auth, authChange)
		apply(&want.MTLS, setChange)
		for i := range want.Tests {
			t := &want.Tests[i]
			apply(&t.Auth, authChange)
			apply(&t.MTLS, setChange)
			t.Variants = append([]model.Variant{}, t.Variants...)
			for j := range t.Variants {
				apply(&t.Variants[j].Auth, authChange)
				apply(&t.Variants[j].MTLS, setChange)
			}
		}
		// Edit the text so comments and layout survive; fall back to
		// rewriting the document if that did not produce the right result.
		path, _ := s.scenarioPath(app, id)
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out := raw
		for _, old := range sortedMapKeys(authChange) {
			out = replaceRef(out, "auth", old, authChange[old])
		}
		for _, old := range sortedMapKeys(setChange) {
			out = replaceRef(out, "mtls", old, setChange[old])
		}
		var got model.Scenario
		if yaml.UnmarshalWithOptions(out, &got, yaml.Strict()) == nil && sameRefs(&got, &want) {
			err = writeFile(path, out, 0o644)
		} else {
			err = writeYAML(path, &want)
		}
		if err != nil {
			return err
		}
		m.note("%s: scenario %s now refers to global auth and certificates", app, id)
	}
	return nil
}

func sortedMapKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// replaceRef changes "key: old" lines to "key: new", keeping indentation,
// list dashes, quotes and trailing comments.
func replaceRef(raw []byte, key, old, new string) []byte {
	re := regexp.MustCompile(`(?m)^([ \t]*(?:-[ \t]+)?` + key + `:[ \t]*["']?)` + regexp.QuoteMeta(old) + `(["']?[ \t]*(?:#.*)?)$`)
	return re.ReplaceAll(raw, []byte("${1}"+new+"${2}"))
}

func sameRefs(a, b *model.Scenario) bool {
	if a.Auth != b.Auth || a.MTLS != b.MTLS || len(a.Tests) != len(b.Tests) {
		return false
	}
	for i := range a.Tests {
		x, y := &a.Tests[i], &b.Tests[i]
		if x.Auth != y.Auth || x.MTLS != y.MTLS || len(x.Variants) != len(y.Variants) {
			return false
		}
		for j := range x.Variants {
			if x.Variants[j].Auth != y.Variants[j].Auth || x.Variants[j].MTLS != y.Variants[j].MTLS {
				return false
			}
		}
	}
	return true
}
