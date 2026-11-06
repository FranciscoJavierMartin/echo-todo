// Package config loads and validates process configuration from the environment.
// It is the only package allowed to read environment variables.
package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Env             string        `env:"APP_ENV"` // dev|test|prod
	Addr            string        `env:"HTTP_ADDR"`
	DatabaseURL     string        `env:"DATABASE_URL"`
	RedisURL        string        `env:"REDIS_URL"`
	JWTSecret       []byte        `env:"JWT_SECRET"`
	AccessTTL       time.Duration `env:"ACCESS_TTL"`
	RefreshTTL      time.Duration `env:"REFRESH_TTL"`
	AllowedOrigins  []string      `env:"CORS_ORIGINS"`
	TrustedProxies  []string      `env:"TRUSTED_PROXIES"`
	ExposeErrors    bool          `env:"EXPOSE_ERRORS"`
	MaxUploadBytes  int64         `env:"MAX_UPLOAD_BYTES"`
	MaxBodyBytes    int64         `env:"MAX_BODY_BYTES"` // JSON request bodies
	RequestsPerSec  float64       `env:"RATE_LIMIT_RPS"`
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT"`
	OTLPEndpoint    string        `env:"OTEL_EXPORTER_OTLP_ENDPOINT"` // empty → tracing disabled
	MetricsToken    string        `env:"METRICS_TOKEN"`               // optional bearer token for /metrics
	Argon2MemoryKiB uint32        `env:"ARGON2_MEMORY_KIB"`
	Argon2Time      uint32        `env:"ARGON2_ITERATIONS"`
	Argon2Threads   uint8         `env:"ARGON2_PARALLELISM"`
	AuthRPS         float64       `env:"AUTH_RATE_LIMIT_RPS"`  // per IP on /v1/auth/*
	WriteRPS        float64       `env:"WRITE_RATE_LIMIT_RPS"` // per user on mutating todo routes
	RequestTimeout  time.Duration `env:"REQUEST_TIMEOUT"`
	DBQueryTimeout  time.Duration `env:"DB_QUERY_TIMEOUT"`
	UploadDir       string        `env:"UPLOAD_DIR"`
}

// Load reads the environment, applies defaults and validates the result.
// All problems are reported at once so a misconfigured deploy fails loudly on boot.
func Load() (Config, error) {
	return load(os.LookupEnv)
}

// DatabaseURL returns DATABASE_URL only, for tools (migrations) that need nothing else.
func DatabaseURL() (string, error) {
	v, ok := os.LookupEnv("DATABASE_URL")
	if !ok || strings.TrimSpace(v) == "" {
		return "", errors.New("DATABASE_URL is required")
	}
	return strings.TrimSpace(v), nil
}

// ParseCIDR accepts "10.0.0.0/8" or a bare IP ("127.0.0.1" → /32, "::1" → /128).
func ParseCIDR(s string) (*net.IPNet, error) {
	if !strings.Contains(s, "/") {
		ip := net.ParseIP(s)
		if ip == nil {
			return nil, fmt.Errorf("invalid IP or CIDR %q", s)
		}
		bits := 128
		if ip.To4() != nil {
			ip, bits = ip.To4(), 32
		}
		return &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)}, nil
	}
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		return nil, fmt.Errorf("invalid IP or CIDR %q", s)
	}
	return n, nil
}

// Addr returns HTTP_ADDR (default :8080) for the container healthcheck probe.
func Addr() string {
	if v, ok := os.LookupEnv("HTTP_ADDR"); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return ":8080"
}

type lookupFunc func(string) (string, bool)

func load(lookup lookupFunc) (Config, error) {
	p := parser{lookup: lookup}
	cfg := Config{
		Env:             p.str("APP_ENV", "dev"),
		Addr:            p.str("HTTP_ADDR", ":8080"),
		DatabaseURL:     p.str("DATABASE_URL", ""),
		RedisURL:        p.str("REDIS_URL", ""),
		JWTSecret:       []byte(p.str("JWT_SECRET", "")),
		AccessTTL:       p.duration("ACCESS_TTL", 10*time.Minute),
		RefreshTTL:      p.duration("REFRESH_TTL", 30*24*time.Hour),
		AllowedOrigins:  p.list("CORS_ORIGINS", nil),
		TrustedProxies:  p.list("TRUSTED_PROXIES", nil),
		ExposeErrors:    p.boolean("EXPOSE_ERRORS", false),
		MaxUploadBytes:  p.int64("MAX_UPLOAD_BYTES", 10<<20),
		MaxBodyBytes:    p.int64("MAX_BODY_BYTES", 1<<20),
		RequestsPerSec:  p.float("RATE_LIMIT_RPS", 20),
		ShutdownTimeout: p.duration("SHUTDOWN_TIMEOUT", 15*time.Second),
		OTLPEndpoint:    p.str("OTEL_EXPORTER_OTLP_ENDPOINT", ""),
		MetricsToken:    p.str("METRICS_TOKEN", ""),
		Argon2MemoryKiB: uint32(p.int64("ARGON2_MEMORY_KIB", 64*1024)), // #nosec G115 -- validated below
		Argon2Time:      uint32(p.int64("ARGON2_ITERATIONS", 3)),       // #nosec G115
		Argon2Threads:   uint8(p.int64("ARGON2_PARALLELISM", 2)),       // #nosec G115
		AuthRPS:         p.float("AUTH_RATE_LIMIT_RPS", 5),
		WriteRPS:        p.float("WRITE_RATE_LIMIT_RPS", 10),
		RequestTimeout:  p.duration("REQUEST_TIMEOUT", 10*time.Second),
		DBQueryTimeout:  p.duration("DB_QUERY_TIMEOUT", 3*time.Second),
		UploadDir:       p.str("UPLOAD_DIR", "./data/uploads"),
	}
	errs := p.errs
	errs = append(errs, cfg.validate()...)
	if len(errs) > 0 {
		return Config{}, fmt.Errorf("invalid configuration: %w", errors.Join(errs...))
	}
	return cfg, nil
}

func (c Config) validate() []error {
	var errs []error
	switch c.Env {
	case "dev", "test", "prod":
	default:
		errs = append(errs, fmt.Errorf("APP_ENV must be one of dev|test|prod, got %q", c.Env))
	}
	if c.Addr == "" {
		errs = append(errs, errors.New("HTTP_ADDR must not be empty"))
	}
	if c.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL is required"))
	}
	if len(c.JWTSecret) == 0 {
		errs = append(errs, errors.New("JWT_SECRET is required"))
	} else if len(c.JWTSecret) < 32 {
		errs = append(errs, errors.New("JWT_SECRET must be at least 32 bytes"))
	}
	if c.AccessTTL <= 0 || c.RefreshTTL <= 0 {
		errs = append(errs, errors.New("ACCESS_TTL and REFRESH_TTL must be positive"))
	}
	if c.AccessTTL >= c.RefreshTTL {
		errs = append(errs, errors.New("ACCESS_TTL must be shorter than REFRESH_TTL"))
	}
	if c.MaxUploadBytes <= 0 || c.MaxBodyBytes <= 0 {
		errs = append(errs, errors.New("MAX_UPLOAD_BYTES and MAX_BODY_BYTES must be positive"))
	}
	if c.RequestsPerSec <= 0 || c.AuthRPS <= 0 || c.WriteRPS <= 0 {
		errs = append(errs, errors.New("RATE_LIMIT_RPS, AUTH_RATE_LIMIT_RPS and WRITE_RATE_LIMIT_RPS must be positive"))
	}
	if c.RequestTimeout <= 0 || c.DBQueryTimeout <= 0 {
		errs = append(errs, errors.New("REQUEST_TIMEOUT and DB_QUERY_TIMEOUT must be positive"))
	}
	for _, o := range c.AllowedOrigins {
		if o == "*" || !strings.HasPrefix(o, "http://") && !strings.HasPrefix(o, "https://") {
			errs = append(errs, fmt.Errorf("CORS_ORIGINS: %q must be an explicit http(s) origin (no wildcard with credentials)", o))
		}
	}
	if c.ShutdownTimeout <= 0 {
		errs = append(errs, errors.New("SHUTDOWN_TIMEOUT must be positive"))
	}
	if c.Argon2MemoryKiB < 1024 || c.Argon2Time < 1 || c.Argon2Threads < 1 {
		errs = append(errs, errors.New("ARGON2_* parameters too small"))
	}
	if c.Env == "prod" && (c.Argon2MemoryKiB < 19*1024 || c.Argon2Time < 2) {
		errs = append(errs, errors.New("ARGON2_* parameters below OWASP minimum for prod"))
	}
	for _, p := range c.TrustedProxies {
		if _, err := ParseCIDR(p); err != nil {
			errs = append(errs, fmt.Errorf("TRUSTED_PROXIES: %w", err))
		}
	}
	if c.Env == "prod" && c.ExposeErrors {
		errs = append(errs, errors.New("EXPOSE_ERRORS must be false in prod"))
	}
	return errs
}

type parser struct {
	lookup lookupFunc
	errs   []error
}

func (p *parser) raw(key string) (string, bool) {
	v, ok := p.lookup(key)
	if !ok {
		return "", false
	}
	v = strings.TrimSpace(v)
	return v, v != ""
}

func (p *parser) str(key, def string) string {
	if v, ok := p.raw(key); ok {
		return v
	}
	return def
}

func (p *parser) list(key string, def []string) []string {
	v, ok := p.raw(key)
	if !ok {
		return def
	}
	var out []string
	for item := range strings.SplitSeq(v, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func (p *parser) duration(key string, def time.Duration) time.Duration {
	v, ok := p.raw(key)
	if !ok {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		p.errs = append(p.errs, fmt.Errorf("%s: %w", key, err))
		return def
	}
	return d
}

func (p *parser) boolean(key string, def bool) bool {
	v, ok := p.raw(key)
	if !ok {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		p.errs = append(p.errs, fmt.Errorf("%s: %w", key, err))
		return def
	}
	return b
}

func (p *parser) int64(key string, def int64) int64 {
	v, ok := p.raw(key)
	if !ok {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		p.errs = append(p.errs, fmt.Errorf("%s: %w", key, err))
		return def
	}
	return n
}

func (p *parser) float(key string, def float64) float64 {
	v, ok := p.raw(key)
	if !ok {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		p.errs = append(p.errs, fmt.Errorf("%s: %w", key, err))
		return def
	}
	return f
}
