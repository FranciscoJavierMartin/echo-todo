package server

// pprof registers on http.DefaultServeMux. The server never serves DefaultServeMux
// directly; it is only reachable through the admin-guarded /debug/pprof/* route.
import _ "net/http/pprof" // #nosec G108 -- mounted behind RequireRole("admin")
