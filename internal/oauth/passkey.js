// Passkey ceremonies for the NAS sign-in and passkey pages.
// A form with data-passkey="get" or "create" and data-options="<url>" fetches
// WebAuthn options from the server, asks the browser for a credential, and
// submits the result in the hidden "credential" field.
(function () {
  "use strict";

  function decode(text) {
    var s = text.replace(/-/g, "+").replace(/_/g, "/");
    while (s.length % 4) { s += "="; }
    var raw = atob(s);
    var bytes = new Uint8Array(raw.length);
    for (var i = 0; i < raw.length; i++) { bytes[i] = raw.charCodeAt(i); }
    return bytes.buffer;
  }

  function encode(buffer) {
    var bytes = new Uint8Array(buffer);
    var raw = "";
    for (var i = 0; i < bytes.length; i++) { raw += String.fromCharCode(bytes[i]); }
    return btoa(raw).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  }

  function describe(error) {
    var name = error && error.name;
    if (name === "NotAllowedError") {
      return "The passkey request was cancelled or timed out. Try again.";
    }
    if (name === "InvalidStateError") {
      return "This device already has a passkey for the NAS. Use a different device or security key.";
    }
    if (name === "SecurityError") {
      return "This browser refused the passkey because the address does not match the NAS.";
    }
    return (error && error.message) || "The passkey step failed. Try again.";
  }

  function decodeList(list) {
    return (list || []).map(function (item) {
      item.id = decode(item.id);
      return item;
    });
  }

  function fromOptions(kind, options) {
    var key = options.publicKey;
    key.challenge = decode(key.challenge);
    if (kind === "create") {
      key.user.id = decode(key.user.id);
      key.excludeCredentials = decodeList(key.excludeCredentials);
    } else {
      key.allowCredentials = decodeList(key.allowCredentials);
    }
    return options;
  }

  function creationResult(credential) {
    var response = credential.response;
    return {
      id: credential.id,
      rawId: encode(credential.rawId),
      type: credential.type,
      response: {
        clientDataJSON: encode(response.clientDataJSON),
        attestationObject: encode(response.attestationObject)
      },
      clientExtensionResults: {}
    };
  }

  function assertionResult(credential) {
    var response = credential.response;
    return {
      id: credential.id,
      rawId: encode(credential.rawId),
      type: credential.type,
      response: {
        clientDataJSON: encode(response.clientDataJSON),
        authenticatorData: encode(response.authenticatorData),
        signature: encode(response.signature),
        userHandle: response.userHandle ? encode(response.userHandle) : ""
      },
      clientExtensionResults: {}
    };
  }

  function run(form, status, button) {
    var kind = form.getAttribute("data-passkey");
    if (!window.PublicKeyCredential || !navigator.credentials) {
      status.textContent = "This browser does not support passkeys. Try Chrome, Safari, Edge or Firefox.";
      return;
    }
    button.disabled = true;
    status.textContent = kind === "create"
      ? "Follow your browser's prompt to create the passkey. On Linux, Chrome shows a QR code: scan it with your phone."
      : "Follow your browser's prompt. Use your iPhone, Mac or security key. On Linux, Chrome shows a QR code: scan it with your phone.";
    var body = new URLSearchParams();
    new FormData(form).forEach(function (value, name) {
      if (name !== "credential" && typeof value === "string") { body.append(name, value); }
    });
    fetch(form.getAttribute("data-options"), {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: body.toString()
    }).then(function (response) {
      if (!response.ok) {
        throw new Error(response.status === 429
          ? "Too many attempts. Start the sign-in again later."
          : "The NAS refused the passkey request. Start the sign-in again.");
      }
      return response.json();
    }).then(function (options) {
      var wrapped = fromOptions(kind, options);
      return kind === "create" ? navigator.credentials.create(wrapped) : navigator.credentials.get(wrapped);
    }).then(function (credential) {
      if (!credential) { throw new Error("No passkey was returned."); }
      var result = kind === "create" ? creationResult(credential) : assertionResult(credential);
      form.elements.credential.value = JSON.stringify(result);
      status.textContent = "Checking the passkey...";
      HTMLFormElement.prototype.submit.call(form);
    }).catch(function (error) {
      button.disabled = false;
      status.textContent = describe(error);
    });
  }

  document.addEventListener("DOMContentLoaded", function () {
    var forms = document.querySelectorAll("form[data-passkey]");
    var status = document.getElementById("passkey-status");
    Array.prototype.forEach.call(forms, function (form) {
      var button = form.querySelector("button");
      form.addEventListener("submit", function (event) {
        event.preventDefault();
        run(form, status, button);
      });
    });
  });
})();
