package proxy

import (
	"artifacts-proxy/pkg/cache"
	"artifacts-proxy/pkg/config"
	"artifacts-proxy/pkg/utils"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/sethvargo/go-limiter/memorystore"
)

var ErrMissing = errors.New("artifact missing")

type UpstreamType string

const (
	Npm   UpstreamType = "npm"
	Pypi  UpstreamType = "pypi"
	Maven UpstreamType = "maven"
	Apt   UpstreamType = "apt"
)

func ParseType(typ string) (UpstreamType, error) {
	switch typ {
	case "npm":
		return Npm, nil
	case "pypi":
		return Pypi, nil
	case "maven":
		return Maven, nil
	case "apt":
		return Apt, nil
	}

	return "", fmt.Errorf("unknown type")
}

type upstream struct {
	typ                  UpstreamType
	name                 string
	path                 string
	upstreamURL          string
	authenticationHeader *string
	fallback             *upstream
	metadataMaxAge       time.Duration
	contentMaxAge        time.Duration
}

var artifactExtensions = []string{
	".tgz", ".tar.gz",
	".jar", ".pom",
	".whl", ".egg",
	".deb",
}

func IsArtifactURL(path string) bool {
	lower := strings.ToLower(path)
	for _, ext := range artifactExtensions {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

func isStale(meta *cache.Metadata, up *upstream, urlPath string) bool {
	lastUpdated, err := time.Parse(time.RFC3339, meta.LastUpdated)
	if err != nil {
		return true
	}
	maxAge := up.metadataMaxAge
	if IsArtifactURL(urlPath) {
		maxAge = up.contentMaxAge
	}
	return time.Since(lastUpdated) > maxAge
}

var httpClient = &http.Client{
	Timeout: 120 * time.Second,
}

func proxyUpstream(config *config.Config, up *upstream, req *http.Request) (*http.Response, error) {
	if !config.EnableUpstreams {
		return &http.Response{
			StatusCode: http.StatusBadGateway,
			Body:       http.NoBody,
		}, nil
	}

	upstreamBase, err := url.Parse(up.upstreamURL)
	if err != nil {
		return nil, fmt.Errorf("parsing upstream URL: %w", err)
	}

	suffix := strings.TrimPrefix(req.URL.Path, up.path)

	outURL := *upstreamBase
	outURL.Path = strings.TrimRight(upstreamBase.Path, "/") + suffix
	outURL.RawQuery = req.URL.RawQuery

	outReq, err := http.NewRequestWithContext(req.Context(), req.Method, outURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("creating upstream request: %w", err)
	}

	// Specify user agent to be nice. Additionally, some upstreams might throttle requests without one.
	outReq.Header.Set("User-Agent", "ArtifactsProxy (+https://github.com/deanrock/artifacts-proxy)")

	// Python's Simple repository API supports either application/vnd.pypi.simple.v1+html
	// (same as text/html) or application/vnd.pypi.simple.v1+json.
	//
	// Here we force HTML, since it might be a bit more supported, and to ensure that a single
	// content type is always cached.
	if up.typ == Pypi {
		outReq.Header.Set("Content-Type", "text/html")
	}

	if up.authenticationHeader != nil {
		outReq.Header.Set("Authorization", *up.authenticationHeader)
	} else {
		outReq.Header.Del("Authorization")
	}

	return httpClient.Do(outReq)
}

func getCachedItem(ctx context.Context, config *config.Config, logger *slog.Logger, s3c *s3Store, up *upstream, req *http.Request, params cache.Params, hash string) (*http.Response, error) {
	cacheDir := config.CacheDir
	cachePath := filepath.Join(cacheDir, hash)
	metaPath := cachePath + ".metadata"

	serveLocal := func(typ string) (*http.Response, error) {
		logger.Debug("serving local", slog.String("type", typ))

		meta, err := readMetadata(metaPath)
		if err != nil {
			return nil, err
		}
		f, err := os.Open(cachePath)
		if err != nil {
			return nil, err
		}
		resp := &http.Response{
			StatusCode: meta.StatusCode,
			Header:     make(http.Header),
			Body:       f,
		}
		if meta.ContentType != nil && *meta.ContentType != "" {
			resp.Header.Set("Content-Type", *meta.ContentType)
		}
		return resp, nil
	}

	serveStale := func(typ string) (*http.Response, error) {
		if fileExists(cachePath) && fileExists(metaPath) {
			return serveLocal(typ)
		}

		if s3c != nil {
			err := s3c.fetch(ctx, hash, cacheDir)
			if err == nil {
				return serveLocal(typ)
			}

			if !isS3NotFound(err) {
				logger.Error("failed fetching from S3", slog.String("err", err.Error()))
				return nil, errors.Wrap(err, "failed fetching stale from S3")
			}
		}

		return nil, ErrMissing
	}

	cache := func(upstreamResp *http.Response) error {
		ct := upstreamResp.Header.Get("Content-Type")
		var ctPtr *string
		if ct != "" {
			ctPtr = &ct
		}

		meta := cache.Metadata{
			Params:      params,
			ContentType: ctPtr,
			StatusCode:  upstreamResp.StatusCode,
			LastUpdated: time.Now().Format(time.RFC3339),
		}

		metaData, err := json.Marshal(meta)
		if err != nil {
			return err
		}
		if err := atomicWriteBytes(filepath.Join(cacheDir, "tmp"), metaPath, metaData); err != nil {
			return err
		}

		if up.typ == Pypi && ct == "text/html" {
			body, err := io.ReadAll(upstreamResp.Body)
			if err != nil {
				return err
			}
			rewritten := strings.ReplaceAll(string(body), "https://files.pythonhosted.org/", "/pythonhosted/")
			if err := atomicWriteBytes(filepath.Join(cacheDir, "tmp"), cachePath, []byte(rewritten)); err != nil {
				return err
			}
		} else {
			if err := atomicWriteReader(filepath.Join(cacheDir, "tmp"), cachePath, upstreamResp.Body); err != nil {
				return err
			}
		}

		if s3c != nil {
			go s3c.upload(context.Background(), hash, cacheDir)
		}

		return nil
	}

	// Serve locally if not stale.
	if fileExists(cachePath) && fileExists(metaPath) {
		localMeta, err := readMetadata(metaPath)
		if err == nil {
			if !isStale(localMeta, up, params.URL) {
				return serveLocal("local")
			}
		}
	}

	// Fetch from S3 if not stale.
	if s3c != nil {
		if s3Meta, err := s3c.getMetadata(ctx, hash); err != nil {
			if !isS3NotFound(err) {
				logger.Error("failed fetching from S3", slog.String("err", err.Error()))
			}
		} else if !isStale(s3Meta, up, params.URL) {
			if err := s3c.fetch(ctx, hash, cacheDir); err != nil {
				logger.Error("failed fetching from S3", slog.String("err", err.Error()))
			}
			return serveLocal("refreshed-from-s3")
		}
	}

	// Fetch from upstream. On failure, serve stale if exists.
	logger.Debug("refreshing from upstream")
	upstreamResp, err := proxyUpstream(config, up, req)
	if err != nil {
		logger.Error("refreshing from upstream failed", slog.String("err", err.Error()))
		return serveStale("upstream-failed")
	}
	defer upstreamResp.Body.Close()

	switch upstreamResp.StatusCode {
	case http.StatusOK, http.StatusNotFound:
		if err := cache(upstreamResp); err != nil {
			return nil, errors.Wrap(err, "error while caching upstream response")
		}
		return serveLocal("local-after-refresh")
	default:
		logger.Warn("refreshing from upstream failed; unexpected status code", slog.Int("code", upstreamResp.StatusCode))
		return serveStale("stale-upstream-failed")
	}
}

func getCachedItemWithFallback(config *config.Config, s3c *s3Store, up *upstream, req *http.Request) (*http.Response, error) {
	params := cache.Params{
		UpstreamName: up.name,
		URL:          req.URL.RequestURI(),
		Method:       req.Method,
	}

	hash, err := params.Hash()
	if err != nil {
		return nil, err
	}

	logger := slog.With(slog.String("upstream", up.name), slog.String("url", params.URL), slog.String("hash", hash))
	logger.Debug("fetching cached item")

	resp, err := getCachedItem(req.Context(), config, logger, s3c, up, cloneRequestHead(req), params, hash)
	if err != nil && !errors.Is(err, ErrMissing) {
		return nil, err
	}

	if up.fallback != nil && (errors.Is(err, ErrMissing) || resp.StatusCode == http.StatusNotFound) {
		resp.Body.Close()
		return getCachedItemWithFallback(config, s3c, up.fallback, cloneRequestHead(req))
	}

	// Return 502 if URL is not cached and cannot be successfully fetched.
	if errors.Is(err, ErrMissing) {
		return &http.Response{
			StatusCode: http.StatusBadGateway,
			Body:       http.NoBody,
		}, nil
	}
	return resp, err
}

func cloneRequestHead(req *http.Request) *http.Request {
	newReq := req.Clone(req.Context())
	newReq.Body = nil
	newReq.ContentLength = 0
	return newReq
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func atomicWriteBytes(tmpDir, dest string, data []byte) error {
	f, err := os.CreateTemp(tmpDir, "atomic-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_, writeErr := f.Write(data)
	f.Close()
	if writeErr != nil {
		os.Remove(name)
		return writeErr
	}
	return os.Rename(name, dest)
}

func atomicWriteReader(tmpDir, dest string, r io.Reader) error {
	f, err := os.CreateTemp(tmpDir, "atomic-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_, copyErr := io.Copy(f, r)
	f.Close()
	if copyErr != nil {
		os.Remove(name)
		return copyErr
	}
	return os.Rename(name, dest)
}

func readMetadata(path string) (*cache.Metadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var meta cache.Metadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		slog.Info("request handled", slog.String("url", r.URL.Path), slog.String("method", r.Method), slog.Int("code", rec.status), slog.String("duration", time.Since(start).String()))
	})
}

func basicAuthMiddlewareFactory(auth config.ConfigAuth, xForwardedForEnabled bool) (func(next http.Handler) http.Handler, error) {
	// Throttle requests with invalid credentials.
	store, err := memorystore.New(&memorystore.Config{
		Tokens:   10,
		Interval: time.Minute,
	})
	if err != nil {
		return nil, err
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeUnauthorizedResponse := func() {
				w.Header().Set("WWW-Authenticate", "Basic realm=\""+auth.Realm+"\"")
				http.Error(w, "unauthorized", http.StatusUnauthorized)
			}

			user, pass, ok := r.BasicAuth()
			if !ok {
				writeUnauthorizedResponse()
				return
			}

			if user != auth.Username || pass != auth.Password {
				ip, err := utils.GetRequestIP(xForwardedForEnabled, r)
				if err != nil {
					slog.Error("[authMiddleware] cannot fetch request IP", slog.String("err", err.Error()))
					http.Error(w, "server error", http.StatusInternalServerError)
					return
				}
				_, _, _, ok, err = store.Take(r.Context(), ip)
				if err != nil {
					slog.Error("[authMiddleware] rate limiter error", slog.String("err", err.Error()))
					http.Error(w, "server error", http.StatusInternalServerError)
					return
				}

				if !ok {
					slog.Warn("[authMiddleware] throttled", slog.String("ip", ip))
					http.Error(w, "too many requests", http.StatusTooManyRequests)
					return
				}

				writeUnauthorizedResponse()
				return
			}
			next.ServeHTTP(w, r)
		})
	}, nil
}

func RunServer(listener net.Listener, config *config.Config) error {
	tmpPath := filepath.Join(config.CacheDir, "tmp")
	if err := os.MkdirAll(tmpPath, 0755); err != nil {
		return fmt.Errorf("creating tmp dir: %w", err)
	}

	var s3c *s3Store
	if config.S3 != nil {
		var err error
		s3c, err = newS3Store(config.S3)
		if err != nil {
			return fmt.Errorf("initializing S3: %w", err)
		}
		slog.Info("S3 storage enabled", slog.String("bucket", config.S3.Bucket), slog.String("region", config.S3.Region))
	}

	upstreams := make(map[string]*upstream)
	for name, cfg := range config.Upstreams {
		if strings.HasSuffix(cfg.Path, "/") {
			return fmt.Errorf("upstream path must not end with a slash: %s", cfg.Path)
		}

		metadataMaxAge, _ := time.ParseDuration(cfg.MetadataMaxAge)
		contentMaxAge, _ := time.ParseDuration(cfg.ContentMaxAge)

		typ, err := ParseType(cfg.Type)
		if err != nil {
			return errors.Wrapf(err, "failed to parse type '%s'", cfg.Type)
		}
		up := &upstream{
			typ:                  typ,
			name:                 name,
			path:                 cfg.Path,
			upstreamURL:          cfg.UpstreamURL,
			authenticationHeader: cfg.AuthenticationHeader,
			metadataMaxAge:       metadataMaxAge,
			contentMaxAge:        contentMaxAge,
		}
		if cfg.Fallback != nil {
			up.fallback = &upstream{
				name:                 name + "-fallback",
				path:                 cfg.Path,
				upstreamURL:          cfg.Fallback.UpstreamURL,
				authenticationHeader: cfg.Fallback.AuthenticationHeader,
				metadataMaxAge:       metadataMaxAge,
				contentMaxAge:        contentMaxAge,
			}
		}
		upstreams[name] = up
	}

	if _, exists := upstreams["pythonhosted"]; exists {
		return fmt.Errorf("pythonhosted is a reserved hardcoded upstream")
	}
	upstreams["pythonhosted"] = &upstream{
		name:           "pythonhosted",
		path:           "/pythonhosted",
		upstreamURL:    "https://files.pythonhosted.org",
		metadataMaxAge: 5 * time.Minute,
		contentMaxAge:  24 * time.Hour,
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})

	authMiddleware, err := basicAuthMiddlewareFactory(config.Auth, config.TrustXForwardedFor)
	if err != nil {
		return err
	}

	for _, up := range upstreams {
		// In case of `/pypi/simple -> https://pypi.org/simple` we need to support proxying both `/pypi/simple/` as well as just `/pypi/simple`.
		for _, url := range []string{up.path, up.path + "/"} {
			mux.Handle("GET "+url, authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				resp, err := getCachedItemWithFallback(config, s3c, up, r)
				if err != nil {
					slog.Error("request failed", slog.String("url", r.URL.Path), slog.String("err", err.Error()))
					http.Error(w, "internal server error", http.StatusInternalServerError)
					return
				}
				defer resp.Body.Close()
				for k, vv := range resp.Header {
					for _, v := range vv {
						w.Header().Add(k, v)
					}
				}
				w.WriteHeader(resp.StatusCode)
				io.Copy(w, resp.Body)
			})))
		}
	}

	// Default route should apply auth to prevent route discovery.
	mux.Handle("/", authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	})))

	server := &http.Server{Handler: loggingMiddleware(mux)}

	if config.TLS != nil {
		cert, err := tls.LoadX509KeyPair(config.TLS.CertPath, config.TLS.KeyPath)
		if err != nil {
			return fmt.Errorf("loading TLS cert: %w", err)
		}
		server.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
		return server.ServeTLS(listener, "", "")
	}

	return server.Serve(listener)
}
