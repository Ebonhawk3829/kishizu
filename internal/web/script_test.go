package web

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The UI's JavaScript lives inside the Go template, so `go test` never runs
// it. A syntax error or a temporal-dead-zone bug ships silently and only
// shows up as a broken panel in the browser.
//
// These tests extract the script and evaluate it in Node against a minimal DOM
// stub. They are skipped when node is unavailable, because a missing
// interpreter is not a failure of the code under test.

const templatePath = "templates/index.html"

var reScript = regexp.MustCompile(`(?s)<script>(.*?)</script>`)

// extractScript pulls the inline script out of the template.
func extractScript(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	m := reScript.FindStringSubmatch(string(b))
	if m == nil {
		t.Fatal("no <script> block found in the template")
	}
	return m[1]
}

func nodeAvailable(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not available; skipping JS checks")
	}
	return p
}

// domStub is a minimal browser. It provides only what the script touches at
// load time, which is enough to catch errors in the code that runs on startup
// and in the render paths we can drive directly.
const domStub = `
const store = {};
function mkEl(id) {
  return {
    id,
    value: '',
    textContent: '',
    innerHTML: '',
    open: false,
    checked: false,
    dataset: {},
    style: {},
    classList: {
      _s: new Set(),
      add(c) { this._s.add(c); },
      remove(c) { this._s.delete(c); },
      toggle(c, on) { on ? this._s.add(c) : this._s.delete(c); },
      contains(c) { return this._s.has(c); },
    },
    addEventListener() {},
    focus() {},
    querySelector() { return null; },
    querySelectorAll() { return []; },
    appendChild() {},
    remove() {},
    closest() { return null; },
    getContext() { return null; },
  };
}
globalThis.document = {
  getElementById(id) { return store[id] || (store[id] = mkEl(id)); },
  querySelector() { return null; },
  querySelectorAll() { return []; },
  createElement() { return mkEl('new'); },
  addEventListener() {},
  body: { style: {}, appendChild() {}, classList: mkEl('body').classList },
};
globalThis.window = globalThis;
// Shaped per endpoint, because loadShows() runs on load and calls
// .filter() on the result. Returning {} would throw for the wrong reason and
// mask the errors these tests exist to catch.
globalThis.fetch = (url) => {
  const u = String(url || '');
  let body = {};
  if (u.startsWith('/shows')) body = [];
  else if (u.startsWith('/api/summary')) body = { ready: 0, hunting: 0, missing: 0 };
  else if (u.startsWith('/api/timetable')) body = { fetched: '', count: 0, entries: [] };
  else if (u.startsWith('/api/config')) body = { library: '', naming: {}, quality: {}, downloader: {}, notifier: {}, indexer: {} };
  return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(body) });
};
globalThis.localStorage = { getItem: () => null, setItem() {}, removeItem() {} };
`

// TestScriptParses: the script must be syntactically valid. A stray brace or a
// bad template literal otherwise ships as a completely dead UI.
func TestScriptParses(t *testing.T) {
	node := nodeAvailable(t)
	src := extractScript(t)

	// Parse only: wrap in a function so nothing executes, and let Node report
	// any syntax error.
	cmd := exec.Command(node, "--check", "-")
	cmd.Stdin = strings.NewReader(src)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("script has a syntax error: %v\n%s", err, out)
	}
}

// TestScriptLoadsWithoutError: the script must run to completion against the
// DOM stub. This is what catches temporal-dead-zone bugs — a local `const when`
// shadowing the global when() formatter threw "Cannot access 'when' before
// initialization" and broke the browse panel entirely.
func TestScriptLoadsWithoutError(t *testing.T) {
	node := nodeAvailable(t)
	src := extractScript(t)

	runner := domStub + "\n" + src + "\n"
	cmd := exec.Command(node, "-")
	cmd.Stdin = strings.NewReader(runner)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Errorf("script threw on load: %v\n%s", err, out)
	}
}

// TestScriptDefinesRenderEntryPoints: the functions the UI wires up must
// exist. A rename that misses one call site leaves a panel that does nothing
// when clicked, with no error at load time.
func TestScriptDefinesRenderEntryPoints(t *testing.T) {
	node := nodeAvailable(t)
	src := extractScript(t)

	want := []string{
		"loadShows", "loadSummary", "loadSettings",
		"openBrowse", "closeBrowse", "browse", "applyBrowseFilter", "browseRow",
		"openAdopt", "closeAdopt", "adoptLookup", "adoptConfirm",
		"addShow", "addFromBrowse", "watchedUpTo", "unlatch", "removeShow",
		"openTrain", "renderTrain", "saveSettings",
		"when", "rel", "escapeHtml", "toast", "api", "el",
	}
	check := domStub + "\n" + src + "\n" + `
const missing = ` + "`" + strings.Join(want, " ") + "`" + `.split(' ')
  .filter(n => typeof globalThis[n] !== 'function');
if (missing.length) {
  console.log('MISSING:' + missing.join(','));
  process.exit(1);
}
console.log('OK');
`
	cmd := exec.Command(node, "-")
	cmd.Stdin = strings.NewReader(check)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Errorf("script is missing entry points: %v\n%s", err, out)
	}
}

// TestScriptRendersBrowseRow: the browse row must render without throwing.
// This is the specific path that broke: browseRow called when() while
// declaring a local of the same name.
func TestScriptRendersBrowseRow(t *testing.T) {
	node := nodeAvailable(t)
	src := extractScript(t)

	runner := domStub + "\n" + src + "\n" + `
const row = browseRow({slug: 'a-show', title: 'A Show', image: '', airs_at: '2026-10-04T00:00:00Z', tracked: false});
if (!row.includes('A Show')) { console.log('NO TITLE'); process.exit(1); }
const tracked = browseRow({slug: 'b', title: 'B', image: '', airs_at: '', tracked: true});
if (!tracked.includes('tracked')) { console.log('NO TRACKED MARK'); process.exit(1); }
console.log('OK');
`
	cmd := exec.Command(node, "-")
	cmd.Stdin = strings.NewReader(runner)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Errorf("browseRow threw: %v\n%s", err, out)
	}
}

// TestScriptHasNoShadowedGlobals: a local that shadows a top-level function is
// almost always a bug here, because the script relies on those helpers
// everywhere. A local `const when` shadowing the global when() formatter threw
// "Cannot access 'when' before initialization" and broke the browse panel.
//
// Only TOP-LEVEL functions are checked. A local arrow inside another function
// (like `sect` in loadShows) is not a global, so shadowing it is not possible
// and flagging it would be a false positive.
func TestScriptHasNoShadowedGlobals(t *testing.T) {
	src := extractScript(t)

	// Top-level function declarations: `function name(` at column 0.
	reGlobal := regexp.MustCompile(`(?m)^function\s+([A-Za-z_$][\w$]*)\s*\(`)
	seen := map[string]bool{}
	var globals []string
	for _, m := range reGlobal.FindAllStringSubmatch(src, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			globals = append(globals, m[1])
		}
	}
	if len(globals) == 0 {
		t.Fatal("no top-level functions found; the extraction is probably wrong")
	}

	for _, g := range globals {
		// A local declaration of the same name, anywhere in the script.
		re := regexp.MustCompile(`(?m)^\s*(?:const|let|var)\s+` + regexp.QuoteMeta(g) + `\s*=`)
		if m := re.FindString(src); m != "" {
			t.Errorf("local declaration shadows the global function %q: %s", g, strings.TrimSpace(m))
		}
	}
}

// TestTemplateFilesAreEmbedded: the templates must be where the embed
// directive expects them, or the server fails to start. Cheap guard against a
// rename that leaves go:embed pointing at nothing.
func TestTemplateFilesAreEmbedded(t *testing.T) {
	if _, err := os.Stat(filepath.Join("templates", "index.html")); err != nil {
		t.Fatalf("index.html missing: %v", err)
	}
}
