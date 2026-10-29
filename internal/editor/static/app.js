(function () {
  "use strict";
  var DRAFT = "nas-memory-draft";
  var OUTBOX = "nas-memory-outbox";

  function load(key, fallback) {
    try {
      var raw = window.localStorage.getItem(key);
      return raw ? JSON.parse(raw) : fallback;
    } catch (e) {
      return fallback;
    }
  }
  function save(key, value) {
    try {
      window.localStorage.setItem(key, JSON.stringify(value));
    } catch (e) {}
  }
  function drop(key) {
    try {
      window.localStorage.removeItem(key);
    } catch (e) {}
  }
  function newKey() {
    if (window.crypto && window.crypto.randomUUID) return window.crypto.randomUUID();
    var a = new Uint8Array(16);
    if (window.crypto && window.crypto.getRandomValues) window.crypto.getRandomValues(a);
    return Array.prototype.map.call(a, function (b) { return ("0" + b.toString(16)).slice(-2); }).join("");
  }

  // Show timestamps in the device's local time.
  function localTimes() {
    var els = document.querySelectorAll("time[datetime]");
    for (var i = 0; i < els.length; i++) {
      var d = new Date(els[i].getAttribute("datetime"));
      if (isNaN(d.getTime())) continue;
      els[i].textContent = d.toLocaleString([], { dateStyle: "medium", timeStyle: "short" });
    }
  }

  // Tell the user when they are looking at a saved copy.
  function offlineBanner() {
    var el = document.getElementById("offline");
    if (!el) return;
    function update() { el.hidden = navigator.onLine !== false; }
    window.addEventListener("online", update);
    window.addEventListener("offline", update);
    update();
  }

  function status(msg) {
    var el = document.getElementById("outbox-status");
    if (el) {
      el.textContent = msg;
      el.hidden = !msg;
    }
  }
  function pendingMessage() {
    var n = load(OUTBOX, []).filter(function (x) { return !x.rejected; }).length;
    return n ? n + (n === 1 ? " note is" : " notes are") + " saved on this phone and will send when you are back online." : "";
  }

  function post(payload) {
    return fetch("/new", {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json", Accept: "application/json" },
      body: JSON.stringify(payload)
    });
  }

  var flushing = false;
  function flush() {
    if (flushing) return Promise.resolve();
    var box = load(OUTBOX, []).filter(function (x) { return !x.rejected; });
    if (!box.length || navigator.onLine === false) return Promise.resolve();
    flushing = true;
    var chain = Promise.resolve();
    box.forEach(function (item) {
      chain = chain.then(function () {
        return post(item).then(function (res) {
          var current = load(OUTBOX, []);
          if (res.ok) {
            save(OUTBOX, current.filter(function (x) { return x.idempotency_key !== item.idempotency_key; }));
          } else if (res.status >= 400 && res.status < 500 && res.status !== 408 && res.status !== 429) {
            // Refused by the editor (bad input, scope, sign-in): park it instead of retrying forever.
            // The server refused this note; keep it so nothing is lost, but stop retrying it.
            save(OUTBOX, current.map(function (x) {
              if (x.idempotency_key === item.idempotency_key) x.rejected = true;
              return x;
            }));
          } else {
            throw new Error("send failed " + res.status);
          }
        });
      });
    });
    return chain
      .catch(function () {})
      .then(function () {
        flushing = false;
        status(pendingMessage());
      });
  }

  function noteForm() {
    var form = document.getElementById("note-form");
    if (!form) return;
    var fields = ["type", "project", "scope", "title", "body", "next_actions"];
    var keyInput = form.elements["idempotency_key"];
    var msg = document.getElementById("note-message");

    function say(text) {
      if (msg) { msg.textContent = text; msg.hidden = !text; }
    }
    function snapshot() {
      var d = { idempotency_key: keyInput.value };
      fields.forEach(function (f) { if (form.elements[f]) d[f] = form.elements[f].value; });
      return d;
    }
    function restore() {
      var d = load(DRAFT, null);
      if (d && typeof d === "object") {
        fields.forEach(function (f) { if (form.elements[f] && typeof d[f] === "string") form.elements[f].value = d[f]; });
        if (d.idempotency_key) keyInput.value = d.idempotency_key;
        say("Draft restored.");
      } else {
        // A cached copy of this page can carry an old key, so always start with a fresh one.
        keyInput.value = newKey();
      }
    }
    restore();
    form.addEventListener("input", function () { save(DRAFT, snapshot()); });

    form.addEventListener("submit", function (ev) {
      ev.preventDefault();
      var d = snapshot();
      d.occurred_at = new Date().toISOString();
      d.next_actions = (d.next_actions || "").split("\n").map(function (s) { return s.trim(); }).filter(Boolean);
      function queue() {
        var box = load(OUTBOX, []);
        box.push(d);
        save(OUTBOX, box);
        drop(DRAFT);
        form.reset();
        keyInput.value = newKey();
        say("Saved on this phone; will send when back online.");
        status(pendingMessage());
      }
      if (navigator.onLine === false) { queue(); return; }
      post(d).then(function (res) {
        if (res.ok) {
          return res.json().then(function (j) {
            drop(DRAFT);
            window.location.href = "/e/" + encodeURIComponent(j.id);
          });
        }
        return res.json().catch(function () { return {}; }).then(function (j) {
          say(j.error || "The server did not accept this note (" + res.status + ").");
        });
      }).catch(queue);
    });
  }

  function init() {
    localTimes();
    offlineBanner();
    noteForm();
    status(pendingMessage());
    flush();
    window.addEventListener("online", flush);
    if ("serviceWorker" in navigator) navigator.serviceWorker.register("/sw.js", { scope: "/" }).catch(function () {});
  }
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", init);
  else init();
})();
