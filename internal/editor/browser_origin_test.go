package editor

import (
	"net/http"
	"testing"
)

// formOrigin is the Origin header a browser sends when a form on a page is submitted
// (Fetch standard, mode "navigate"): the literal "null" when the hosting page's
// Referrer-Policy is "no-referrer", otherwise the page's origin for same-origin targets.
func formOrigin(page *http.Response) string {
	if page.Header.Get("Referrer-Policy") == "no-referrer" {
		return "null"
	}
	return testOrigin
}

func TestFormsWorkWithTheOriginARealBrowserSends(t *testing.T) {
	h, f := seeded(t)
	pages := map[string]struct{ target, body string }{
		"/new":                {"/new", form("type", "manual_note", "title", "from the phone", "scope", "personal")},
		"/memories":           {"/memories", ""},
		"/p/alpha/checkpoint": {"/p/alpha/checkpoint", form("expected_version", "2", "summary", "updated")},
	}
	for name, p := range pages {
		page := do(h, "GET", name, "")
		if page.Code != 200 {
			t.Fatalf("%s: %d", name, page.Code)
		}
		if p.body == "" {
			continue
		}
		w := do(h, "POST", p.target, p.body, origin(formOrigin(page.Result())))
		if w.Code == http.StatusForbidden {
			t.Fatalf("%s: a browser's form submission was refused as cross-site: %s", name, w.Body.String())
		}
	}
	if len(f.events) == 3 {
		t.Fatal("the note from the form was not saved")
	}
}

func TestCrossSiteFormsStayRefused(t *testing.T) {
	h, _ := seeded(t)
	for _, o := range []string{"null", "https://evil.example", ""} {
		if w := do(h, "POST", "/new", form("type", "manual_note", "title", "x", "scope", "personal"), origin(o)); w.Code != http.StatusForbidden {
			t.Fatalf("origin %q accepted: %d", o, w.Code)
		}
	}
}
