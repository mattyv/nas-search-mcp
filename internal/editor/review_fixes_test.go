package editor

import (
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func viaFunnel() reqOpt {
	return func(r *http.Request) { r.Header.Set("Tailscale-Funnel-Request", "?1") }
}

func TestMemoryPage(t *testing.T) {
	h, f := seeded(t)
	w := do(h, "GET", "/m/m-cand", "")
	status(t, w, 200)
	mustContain(t, w, "candidate", "fact", "73.0%", `href="/e/ev1"`, `name="to" value="accepted"`, `action="/memories/m-cand/transition"`, `href="/memories/m-cand/forget"`)
	mustNotContain(t, w, "<script>alert")
	w = do(h, "GET", "/m/m-disp", "")
	status(t, w, 200)
	mustContain(t, w, "unscored")
	status(t, do(h, "GET", "/m/nope", ""), 404)
	status(t, do(h, "GET", "/m/m-cand", "", login("mallory@example.com")), 403)
	if f.count("ForgetMemory") != 0 || len(f.corrections) != 0 {
		t.Fatal("viewing a memory changed it")
	}
	// Terminal memories offer no transition but can still be forgotten.
	w = do(h, "GET", "/m/m-rej", "")
	status(t, w, 200)
	mustContain(t, w, `href="/memories/m-rej/forget"`)
	mustNotContain(t, w, `name="to"`)
}

func TestReviewEditProposesUnscored(t *testing.T) {
	h, f := seeded(t)
	status(t, do(h, "POST", "/review/m-cand", form("action", "edit", "statement", "edited")), 303)
	if len(f.proposed) != 1 || f.proposed[0].Probability != -1 {
		t.Fatalf("proposed = %+v", f.proposed)
	}
}

func TestOwnerEntriesUseStoreDefaultStatus(t *testing.T) {
	h, f := seeded(t)
	status(t, do(h, "POST", "/new", form("type", "decision", "scope", "personal", "title", "t")), 303)
	status(t, do(h, "POST", "/e/ev1/correct", form("title", "fixed")), 303)
	for _, e := range f.events[len(f.events)-2:] {
		if e.Status != "" {
			t.Fatalf("editor sent status %q; the store's default must decide", e.Status)
		}
	}
}

func TestFunnelRequestsRefused(t *testing.T) {
	h, f := seeded(t)
	for _, p := range []string{"/", "/memories", "/m/m-acc", "/sw.js", "/static/app.css"} {
		if w := do(h, "GET", p, "", viaFunnel()); w.Code != 403 {
			t.Errorf("%s via funnel: %d", p, w.Code)
		}
	}
	status(t, do(h, "POST", "/new", form("type", "decision", "scope", "personal", "title", "t"), viaFunnel()), 403)
	if len(f.calls) != 0 {
		t.Fatalf("store touched via funnel: %v", f.calls)
	}
}

func TestServiceWorkerBoundsPageCache(t *testing.T) {
	h, _ := seeded(t)
	sw := do(h, "GET", "/sw.js", "").Body.String()
	for _, want := range []string{"MAX_PAGES = 30", "MAX_PAGE_AGE_MS", "trimPages"} {
		if !strings.Contains(sw, want) {
			t.Errorf("service worker lacks %q", want)
		}
	}
	if strings.Count(sw, "trimPages(") < 3 {
		t.Error("trimPages must be defined, run on activate, and run after page writes")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed; behaviour not executed")
	}
	prelude := `globalThis.self = { addEventListener() {}, location: { origin: "https://x" }, clients: { claim() {} }, skipWaiting() {} }; globalThis.caches = {};` + "\n"
	script := `
const stored = new Map();
const cache = {
  keys: async () => [...stored.keys()].map((u) => ({ url: u })),
  match: async (req) => stored.get(req.url),
  delete: async (req) => stored.delete(req.url),
  put: async (req, res) => { stored.set(req.url, res); },
};
(async () => {
  const now = Date.now();
  for (let i = 0; i < 40; i++) stored.set("https://x/p" + i, { headers: { get: () => new Date(now - i * 1000).toUTCString() } });
  stored.set("https://x/old", { headers: { get: () => new Date(now - 8 * 86400000).toUTCString() } });
  await trimPages(cache);
  if (stored.size > 30) throw new Error("size " + stored.size);
  if (stored.has("https://x/old")) throw new Error("old page kept");
  if (!stored.has("https://x/p0")) throw new Error("newest dropped");
  console.log("ok");
})().catch((e) => { console.error(e.message); process.exit(1); });
`
	path := t.TempDir() + "/sw_test.js"
	if err := os.WriteFile(path, []byte(prelude+sw+script), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("node", path).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v %s", err, out)
	}
}
