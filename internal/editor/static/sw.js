// NAS Memory service worker. The server replaces __VERSION__ with a hash of the
// embedded assets, so a new release replaces the old cache.
const VERSION = "__VERSION__";
const STATIC_CACHE = "static-" + VERSION;
const PAGE_CACHE = "pages-v1";
const MAX_PAGES = 30;
const MAX_PAGE_AGE_MS = 7 * 24 * 60 * 60 * 1000;
const PRECACHE = ["/static/app.css", "/static/app.js", "/static/icon-192.png"];

// Cached pages are private notes, so keep few and keep them briefly: drop entries older
// than 7 days, then the oldest beyond 30.
function storedAt(res) {
  const v = res.headers.get("x-cached-at") || res.headers.get("date");
  const n = /^\d+$/.test(v || "") ? Number(v) : Date.parse(v || "");
  return Number.isNaN(n) ? 0 : n;
}

async function trimPages(cache) {
  const now = Date.now();
  const entries = [];
  for (const req of await cache.keys()) {
    const res = await cache.match(req);
    entries.push({ req, at: res ? storedAt(res) : 0 });
  }
  entries.sort((a, b) => b.at - a.at);
  await Promise.all(entries.map((e, i) => (i >= MAX_PAGES || now - e.at > MAX_PAGE_AGE_MS ? cache.delete(e.req) : null)));
}

async function storePage(req, res) {
  const headers = new Headers(res.headers);
  headers.set("x-cached-at", String(Date.now()));
  const cache = await caches.open(PAGE_CACHE);
  await cache.put(req, new Response(await res.blob(), { status: res.status, statusText: res.statusText, headers }));
  await trimPages(cache);
}

self.addEventListener("install", (event) => {
  event.waitUntil(caches.open(STATIC_CACHE).then((c) => c.addAll(PRECACHE)).then(() => self.skipWaiting()));
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((names) => Promise.all(names.filter((n) => n.startsWith("static-") && n !== STATIC_CACHE).map((n) => caches.delete(n))))
      .then(() => caches.open(PAGE_CACHE))
      .then((c) => trimPages(c))
      .then(() => self.clients.claim())
  );
});

self.addEventListener("fetch", (event) => {
  const req = event.request;
  // Never touch writes: they must reach the server or fail visibly.
  if (req.method !== "GET") return;
  const url = new URL(req.url);
  if (url.origin !== self.location.origin) return;

  if (url.pathname.startsWith("/static/")) {
    event.respondWith(
      caches.match(req).then(
        (hit) =>
          hit ||
          fetch(req).then((res) => {
            if (res.ok) {
              const copy = res.clone();
              caches.open(STATIC_CACHE).then((c) => c.put(req, copy));
            }
            return res;
          })
      )
    );
    return;
  }

  if (req.mode === "navigate" || (req.headers.get("accept") || "").includes("text/html")) {
    event.respondWith(
      fetch(req)
        .then((res) => {
          if (res.ok && res.type === "basic") {
            const copy = res.clone();
            event.waitUntil(storePage(req, copy));
          }
          return res;
        })
        .catch(() => caches.match(req).then((hit) => hit || Response.error()))
    );
  }
});
