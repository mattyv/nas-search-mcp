package oauth

// Owner invites add a passkey on a device that has none, without the existing
// passkey. Making one needs a shell on the NAS (`nas-search passkey-invite`),
// which is the owner check; using one still needs the password and ends in a
// passkey registration. Whether an invite is usable and what step it unlocks
// come from the verified core through internal/policy.

import (
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/mattyv/nas-search-mcp/internal/policy"
)

// Flow purposes while an invite is redeemed: idle, and waiting on a registration challenge.
const (
	purposeInvited        = "invited"
	purposeInviteRegister = "invite_register"
)

// CreatePasskeyInvite replaces any earlier invite with a new one-time code that
// lasts the given minutes, and returns the code. Only its hash is stored.
func CreatePasskeyInvite(dbPath string, minutes int) (string, error) {
	if !policy.ValidPasskeyInviteMinutes(minutes) {
		return "", errors.New("invite lifetime must be 1 to 60 minutes")
	}
	raw := make([]byte, 13)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	plain := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)[:20]
	if err := privateDatabase(dbPath); err != nil {
		return "", err
	}
	db, err := sql.Open("sqlite3", sqliteURI(dbPath))
	if err != nil {
		return "", err
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	expires := time.Now().Add(time.Duration(minutes) * time.Minute).Unix()
	for _, q := range []struct {
		sql  string
		args []any
	}{{passkeySchema, nil}, {"DELETE FROM oauth_passkey_invites", nil}, {"INSERT INTO oauth_passkey_invites(hash,expires,used) VALUES(?,?,0)", []any{inviteHash(plain), expires}}} {
		if _, err = tx.Exec(q.sql, q.args...); err != nil {
			return "", err
		}
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return plain[0:5] + "-" + plain[5:10] + "-" + plain[10:15] + "-" + plain[15:20], nil
}

// inviteHash ignores case, spaces and dashes, so a code read off a terminal can be typed loosely.
func inviteHash(code string) string {
	clean := strings.Map(func(r rune) rune {
		if r == '-' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, strings.ToUpper(code))
	return hash("passkey-invite:" + clean)
}

// redeemInvite spends a valid invite and moves this sign-in to registering a new
// passkey. A wrong, used or expired code counts as a failed attempt.
func (s *Server) redeemInvite(w http.ResponseWriter, l *loginState, code string) {
	var expires, used int64
	err := l.tx.QueryRow("SELECT expires,used FROM oauth_passkey_invites WHERE hash=?", inviteHash(code)).Scan(&expires, &used)
	if err != nil || !policy.PasskeyInviteUsable(used != 0, time.Now().Unix(), expires) {
		l.reject(w, stepAssert, "That invite code is wrong, used or expired. Make a new one on the NAS.")
		return
	}
	_, e1 := l.tx.Exec("UPDATE oauth_passkey_invites SET used=? WHERE hash=? AND used=0", time.Now().Unix(), inviteHash(code))
	_, e2 := l.tx.Exec("UPDATE oauth_passkey_flows SET purpose=?,session_data='',challenge_expires=0 WHERE session_hash=?", purposeInvited, hash(l.session))
	if e1 != nil || e2 != nil || l.tx.Commit() != nil {
		oauthError(w, 500, "server_error")
		return
	}
	renderPasskeyStep(w, 200, l.session, stepEnrollRequired, "Invite accepted. Register this device's passkey.")
}
