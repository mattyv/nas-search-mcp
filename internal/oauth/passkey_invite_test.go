package oauth

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func (f *pkFixture) redeem(session, code string) *httptest.ResponseRecorder {
	return f.do("POST", "/authorize/passkey/finish", url.Values{"session": {session}, "invite": {code}})
}

// passwordWithInvite signs in with the password and redeems code at the passkey step.
func (f *pkFixture) passwordWithInvite(code string) (string, *httptest.ResponseRecorder) {
	f.t.Helper()
	session := f.begin()
	if w := f.password(session); w.Code != 200 || !strings.Contains(w.Body.String(), `name="invite"`) {
		f.t.Fatalf("passkey step should offer an invite code: %d %s", w.Code, w.Body.String())
	}
	return session, f.redeem(session, code)
}

func TestInviteAddsASecondPasskeyWithoutTheFirst(t *testing.T) {
	f := newPasskeyFixture(t, "required")
	mac, linux := newSoftKey(t), newSoftKey(t)
	f.enroll(mac)
	code, err := CreatePasskeyInvite(f.s.cfg.DatabasePath, 15)
	if err != nil {
		t.Fatal(err)
	}
	session, w := f.passwordWithInvite(code)
	if w.Code != 200 || completed(w) || !strings.Contains(w.Body.String(), "Register") {
		t.Fatalf("invite should lead to registration: %d %s", w.Code, w.Body.String())
	}
	// An invite never signs in by itself: skipping registration is refused.
	if sk := f.skip(session); completed(sk) {
		t.Fatal("invite plus skip completed sign-in without a passkey")
	}
	session, _ = f.passwordWithInvite(mustInvite(t, f))
	opts := f.options(session)
	if opts.Code != 200 || !strings.Contains(opts.Body.String(), `"challenge"`) || strings.Contains(opts.Body.String(), `"allowCredentials"`) {
		t.Fatalf("invited options should be registration options: %d %s", opts.Code, opts.Body.String())
	}
	if done := f.finish(session, linux.create(t, opts.Body.Bytes())); !completed(done) {
		t.Fatalf("invited registration did not complete sign-in: %d %s", done.Code, done.Body.String())
	}
	if f.passkeyCount() != 2 {
		t.Fatalf("passkeys = %d, want 2", f.passkeyCount())
	}
	// Both devices now sign in on their own.
	if !completed(f.signIn(linux)) || !completed(f.signIn(mac)) {
		t.Fatal("both passkeys should work")
	}
}

func mustInvite(t *testing.T, f *pkFixture) string {
	t.Helper()
	code, err := CreatePasskeyInvite(f.s.cfg.DatabasePath, 15)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func TestInviteWorksOnceAndOnlyTheNewestCounts(t *testing.T) {
	f := newPasskeyFixture(t, "required")
	f.enroll(newSoftKey(t))
	older := mustInvite(t, f)
	code := mustInvite(t, f)
	if _, w := f.passwordWithInvite(older); w.Code != 403 || strings.Contains(w.Body.String(), "Register") {
		t.Fatalf("a replaced invite should fail: %d", w.Code)
	}
	if _, w := f.passwordWithInvite(code); w.Code != 200 {
		t.Fatalf("first use: %d", w.Code)
	}
	if _, w := f.passwordWithInvite(code); w.Code != 403 || !strings.Contains(w.Body.String(), "invite") {
		t.Fatalf("second use should fail with an invite message: %d %s", w.Code, w.Body.String())
	}
}

func TestExpiredOrWrongInviteFailsAndCountsAsAnAttempt(t *testing.T) {
	f := newPasskeyFixture(t, "required")
	f.enroll(newSoftKey(t))
	code := mustInvite(t, f)
	if _, err := f.s.db.Exec("UPDATE oauth_passkey_invites SET expires=?", time.Now().Unix()-1); err != nil {
		t.Fatal(err)
	}
	session, w := f.passwordWithInvite(code)
	if w.Code != 403 || strings.Contains(w.Body.String(), "Register") {
		t.Fatalf("expired invite accepted: %d", w.Code)
	}
	var failures int
	f.s.db.QueryRow("SELECT used FROM oauth_grants WHERE hash=? AND kind='login'", hash(session)).Scan(&failures)
	if failures != 1 {
		t.Fatalf("failed invite should count as a sign-in attempt, got %d", failures)
	}
	if w := f.redeem(session, "AAAAA-AAAAA-AAAAA-AAAAA"); w.Code != 403 {
		t.Fatalf("wrong code: %d", w.Code)
	}
}

func TestInviteCodesAreHashedAndCleared(t *testing.T) {
	f := newPasskeyFixture(t, "required")
	f.enroll(newSoftKey(t))
	code := mustInvite(t, f)
	if len(strings.ReplaceAll(code, "-", "")) < 20 {
		t.Fatalf("code too short: %q", code)
	}
	var stored string
	f.s.db.QueryRow("SELECT hash FROM oauth_passkey_invites").Scan(&stored)
	if stored == "" || strings.Contains(stored, strings.ReplaceAll(code, "-", "")) {
		t.Fatal("invite must be stored hashed")
	}
	if _, err := CreatePasskeyInvite(f.s.cfg.DatabasePath, 0); err == nil {
		t.Fatal("zero-minute invite accepted")
	}
	if err := ResetPasskeys(f.s.cfg.DatabasePath); err != nil {
		t.Fatal(err)
	}
	var n int
	f.s.db.QueryRow("SELECT count(*) FROM oauth_passkey_invites").Scan(&n)
	if n != 0 {
		t.Fatal("reset should clear invites")
	}
}

func TestInviteCodeIgnoresCaseAndSpacing(t *testing.T) {
	f := newPasskeyFixture(t, "required")
	f.enroll(newSoftKey(t))
	code := mustInvite(t, f)
	typed := " " + strings.ToLower(strings.ReplaceAll(code, "-", " ")) + " "
	if _, w := f.passwordWithInvite(typed); w.Code != 200 || !strings.Contains(w.Body.String(), "Register") {
		t.Fatalf("typed code: %d", w.Code)
	}
}
