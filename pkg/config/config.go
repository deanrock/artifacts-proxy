package config

import (
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

type ConfigAuth struct {
	Username string `toml:"username"`
	Password string `toml:"password"`
	// Some package managers (e.g. SBT) require specifying realm name to support authentication.
	Realm string `toml:"realm"`
}

type ConfigTLS struct {
	CertPath string `toml:"cert_path"`
	KeyPath  string `toml:"key_path"`
}

type ConfigUpstreamFallback struct {
	UpstreamURL          string  `toml:"upstream_url"`
	AuthenticationHeader *string `toml:"authentication_header"`
}

type ConfigUpstream struct {
	Type                 string                  `toml:"type"`
	Path                 string                  `toml:"path"`
	UpstreamURL          string                  `toml:"upstream_url"`
	AuthenticationHeader *string                 `toml:"authentication_header"`
	Fallback             *ConfigUpstreamFallback `toml:"fallback"`
	MetadataMaxAge       string                  `toml:"metadata_max_age"`
	ContentMaxAge        string                  `toml:"content_max_age"`
}

type ConfigS3 struct {
	Bucket   string `toml:"bucket"`
	Region   string `toml:"region"`
	Prefix   string `toml:"prefix"`
	Endpoint string `toml:"endpoint"`
}

type Config struct {
	Port               uint16                    `toml:"port"`
	TLS                *ConfigTLS                `toml:"tls"`
	CacheDir           string                    `toml:"cache_dir"`
	EnableUpstreams    bool                      `toml:"enable_upstreams"`
	Upstreams          map[string]ConfigUpstream `toml:"upstreams"`
	Auth               ConfigAuth                `toml:"auth"`
	S3                 *ConfigS3                 `toml:"s3"`
	TrustXForwardedFor bool                      `toml:"trust_x_forwarded_for"`
	PublicOrigin       string                    `toml:"public_origin"`
}

// resolveEnvVars walks all string fields in v and replaces any value starting
// with "env:" with the corresponding environment variable's value.
func resolveEnvVars(v reflect.Value) error {
	switch v.Kind() {
	case reflect.Ptr:
		if v.IsNil() {
			return nil
		}
		return resolveEnvVars(v.Elem())
	case reflect.Struct:
		for i := range v.NumField() {
			if err := resolveEnvVars(v.Field(i)); err != nil {
				return err
			}
		}
	case reflect.Map:
		for _, key := range v.MapKeys() {
			elem := v.MapIndex(key)
			resolved := reflect.New(elem.Type()).Elem()
			resolved.Set(elem)
			if err := resolveEnvVars(resolved); err != nil {
				return err
			}
			v.SetMapIndex(key, resolved)
		}
	case reflect.String:
		s := v.String()
		if strings.HasPrefix(s, "env:") {
			varName := strings.TrimPrefix(s, "env:")
			val, ok := os.LookupEnv(varName)
			if !ok {
				return fmt.Errorf("env var %q (referenced in config) is not set", varName)
			}
			v.SetString(val)
		}
	}
	return nil
}

func ParseFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}
	var config Config
	if _, err := toml.Decode(string(data), &config); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	if err := resolveEnvVars(reflect.ValueOf(&config).Elem()); err != nil {
		return nil, fmt.Errorf("resolving env vars: %w", err)
	}
	if config.Auth.Username == "" || config.Auth.Password == "" {
		return nil, fmt.Errorf("auth username and password are required")
	}
	if config.Auth.Realm == "" {
		config.Auth.Realm = "artifacts-proxy"
	}
	if config.PublicOrigin == "" {
		return nil, fmt.Errorf("public_origin must be in format of 'http(s)://example.com[:3000]'")
	}
	for name, up := range config.Upstreams {
		if !slices.Contains([]string{"npm", "pypi", "maven", "apt"}, up.Type) {
			return nil, fmt.Errorf("upstream %q: type is required; valid options: npm, pypi, maven, apt", name)
		}
		if up.MetadataMaxAge == "" {
			return nil, fmt.Errorf("upstream %q: metadata_max_age is required", name)
		}
		if up.ContentMaxAge == "" {
			return nil, fmt.Errorf("upstream %q: content_max_age is required", name)
		}
		if _, err := time.ParseDuration(up.MetadataMaxAge); err != nil {
			return nil, fmt.Errorf("upstream %q: invalid metadata_max_age: %w", name, err)
		}
		if _, err := time.ParseDuration(up.ContentMaxAge); err != nil {
			return nil, fmt.Errorf("upstream %q: invalid content_max_age: %w", name, err)
		}
	}
	return &config, nil
}
