// A local-only OAuth fixture for the independent Python MCP SDK interoperability test.
package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"github.com/mattyv/nas-search-mcp/internal/oauth"
	"golang.org/x/crypto/bcrypt"
)

func main() {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	issuer := "http://" + listener.Addr().String()
	password, err := bcrypt.GenerateFromPassword([]byte("fixture-password-not-a-real-credential"), 10)
	if err != nil {
		panic(err)
	}
	auth, err := oauth.New(oauth.Config{Issuer: issuer, DatabasePath: filepath.Join(os.Args[1], "oauth.sqlite3"), PasswordHash: string(password), ClientID: "existing-fixture-client"})
	if err != nil {
		panic(err)
	}
	defer auth.Close()
	mux := http.NewServeMux()
	mux.Handle("/mcp", auth.Protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"authenticated": true})
	})))
	mux.Handle("/", auth.Handler())
	fmt.Println(issuer)
	if err := http.Serve(listener, mux); err != nil {
		panic(err)
	}
}
