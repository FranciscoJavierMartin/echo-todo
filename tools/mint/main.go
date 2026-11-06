// Command mint prints an access token for load tests and local debugging:
//
//	JWT_SECRET=... go run ./tools/mint -sub <user-uuid> [-role user] [-ttl 1h]
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"

	"echo-todo-2/internal/auth"
)

func main() {
	sub := flag.String("sub", "", "user id")
	role := flag.String("role", auth.RoleUser, "role")
	ttl := flag.Duration("ttl", time.Hour, "token lifetime")
	flag.Parse()
	secret := os.Getenv("JWT_SECRET")
	id, err := uuid.Parse(*sub)
	if err != nil || len(secret) < 32 {
		fmt.Fprintln(os.Stderr, "usage: JWT_SECRET=<32+ bytes> mint -sub <uuid> [-role user|admin] [-ttl 1h]")
		os.Exit(2)
	}
	tok, _, err := auth.NewTokens([]byte(secret), *ttl).IssueAccess(id, *role)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(tok)
}
