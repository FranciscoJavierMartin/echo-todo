package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func env(m map[string]string) lookupFunc {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func validEnv() map[string]string {
	return map[string]string{
		"DATABASE_URL": "postgres://localhost/todo",
		"JWT_SECRET":   "0123456789abcdef0123456789abcdef",
	}
}

func TestLoad_Defaults(t *testing.T) {
	cfg, err := load(env(validEnv()))
	require.NoError(t, err)
	require.Equal(t, "dev", cfg.Env)
	require.Equal(t, ":8080", cfg.Addr)
	require.Equal(t, 10*time.Minute, cfg.AccessTTL)
	require.Equal(t, int64(10<<20), cfg.MaxUploadBytes)
}

func TestLoad_RequiresDatabaseURLAndSecret(t *testing.T) {
	_, err := load(env(map[string]string{}))
	require.Error(t, err)
	require.ErrorContains(t, err, "DATABASE_URL is required")
	require.ErrorContains(t, err, "JWT_SECRET is required")
}

func TestLoad_ParsesTypes(t *testing.T) {
	m := validEnv()
	m["APP_ENV"] = "prod"
	m["CORS_ORIGINS"] = "https://a.example, https://b.example ,"
	m["ACCESS_TTL"] = "5m"
	m["RATE_LIMIT_RPS"] = "2.5"
	m["MAX_UPLOAD_BYTES"] = "1024"
	cfg, err := load(env(m))
	require.NoError(t, err)
	require.Equal(t, []string{"https://a.example", "https://b.example"}, cfg.AllowedOrigins)
	require.Equal(t, 5*time.Minute, cfg.AccessTTL)
	require.InDelta(t, 2.5, cfg.RequestsPerSec, 0.0001)
	require.Equal(t, int64(1024), cfg.MaxUploadBytes)
}

func TestLoad_RejectsBadValues(t *testing.T) {
	m := validEnv()
	m["ACCESS_TTL"] = "soon"
	m["APP_ENV"] = "staging"
	m["JWT_SECRET"] = "short"
	_, err := load(env(m))
	require.ErrorContains(t, err, "ACCESS_TTL")
	require.ErrorContains(t, err, "APP_ENV")
	require.ErrorContains(t, err, "at least 32 bytes")
}

func TestLoad_ProdForbidsExposeErrors(t *testing.T) {
	m := validEnv()
	m["APP_ENV"] = "prod"
	m["EXPOSE_ERRORS"] = "true"
	_, err := load(env(m))
	require.ErrorContains(t, err, "EXPOSE_ERRORS")
}

func TestTrustedProxies(t *testing.T) {
	m := validEnv()
	m["TRUSTED_PROXIES"] = "10.0.0.0/8,127.0.0.1,::1"
	cfg, err := load(env(m))
	require.NoError(t, err)
	require.Len(t, cfg.TrustedProxies, 3)
	n, err := ParseCIDR("127.0.0.1")
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1/32", n.String())

	m["TRUSTED_PROXIES"] = "not-an-ip"
	_, err = load(env(m))
	require.ErrorContains(t, err, "TRUSTED_PROXIES")
}

func TestCORSOriginsMustBeExplicit(t *testing.T) {
	m := validEnv()
	m["CORS_ORIGINS"] = "*"
	_, err := load(env(m))
	require.ErrorContains(t, err, "CORS_ORIGINS")
}
