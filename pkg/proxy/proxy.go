package proxy

import (
	"artifacts-proxy/pkg/cache"
	"artifacts-proxy/pkg/config"
	"artifacts-proxy/pkg/otel"
	"artifacts-proxy/pkg/utils"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
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

// contextKey is a custom type for context keys to avoid collisions
type contextKey string

// upstreamContextKey is the key for storing upstream name in context
const upstreamContextKey contextKey = "upstream"

// WithUpstream adds the upstream name to the context
func WithUpstream(ctx context.Context, upstream string) context.Context {
	return context.WithValue(ctx, upstreamContextKey, upstream)
}

// GetUpstream retrieves the upstream name from the context
func GetUpstream(ctx context.Context) string {
	if v := ctx.Value(upstreamContextKey); v != nil {
		return v.(string)
	}
	return ""
}

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

func getCachedItem(config *config.Config, s3c *s3Store, up *upstream, req *http.Request) (*http.Response, error) {
	params := cache.Params{
		UpstreamName: up.name,
		URL:          req.URL.RequestURI(),
		Method:       req.Method,
	}

	hash, err := params.Hash()
	if err != nil {
		return nil, err
	}

	cacheDir := config.CacheDir
	cachePath := filepath.Join(cacheDir, hash)
	metaPath := cachePath + ".metadata"

	serveLocal := func(logVerb string) (*http.Response, error) {
		log.Printf("%s %s %s %s", logVerb, up.name, req.URL.RequestURI(), hash)
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

	cacheAndServe := func(upstreamResp *http.Response) (*http.Response, error) {
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
			return nil, err
		}
		if err := atomicWriteBytes(filepath.Join(cacheDir, "tmp"), metaPath, metaData); err != nil {
			return nil, err
		}

		if up.typ == Pypi && ct == "text/html" {
			body, err := io.ReadAll(upstreamResp.Body)
			if err != nil {
				return nil, err
			}
			rewritten := strings.ReplaceAll(string(body), "https://files.pythonhosted.org/", "/pythonhosted/")
			if err := atomicWriteBytes(filepath.Join(cacheDir, "tmp"), cachePath, []byte(rewritten)); err != nil {
				return nil, err
			}
		} else {
			if err := atomicWriteReader(filepath.Join(cacheDir, "tmp"), cachePath, upstreamResp.Body); err != nil {
				return nil, err
			}
		}

		if s3c != nil {
			go s3c.upload(context.Background(), hash, cacheDir)
		}

		f, err := os.Open(cachePath)
		if err != nil {
			return nil, err
		}

		resp := &http.Response{
			StatusCode: upstreamResp.StatusCode,
			Header:     make(http.Header),
			Body:       f,
		}
		if ctPtr != nil && *ctPtr != "" {
			resp.Header.Set("Content-Type", *ctPtr)
		}
		return resp, nil
	}

	if fileExists(cachePath) && fileExists(metaPath) {
		localMeta, metaErr := readMetadata(metaPath)
		if metaErr == nil && !isStale(localMeta, up, req.URL.Path) {
			// Record cache hit with upstream label
			otel.RecordCacheHit(req.Context(), up.name)
			return serveLocal("serving")
		}

		// Stale: check S3 for a newer version before hitting upstream
		if metaErr == nil && s3c != nil {
			if s3Meta, err := s3c.getMetadata(req.Context(), hash); err == nil {
				localTime, _ := time.Parse(time.RFC3339, localMeta.LastUpdated)
				s3Time, s3Err := time.Parse(time.RFC3339, s3Meta.LastUpdated)
				if s3Err == nil && s3Time.After(localTime) {
					if fetchErr := s3c.fetch(req.Context(), hash, cacheDir); fetchErr == nil {
						return serveLocal("s3-refresh")
					}
				}
			}
		}

		// Try upstream; on failure, serve stale
		log.Printf("refreshing %s %s %s", up.name, req.URL.RequestURI(), hash)
		// Record cache miss (stale) with upstream label
		otel.RecordCacheMiss(req.Context(), up.name)
		
		upstreamResp, upstreamErr := proxyUpstream(config, up, req)
		if upstreamErr != nil {
			return serveLocal("serving-stale")
		}
		defer upstreamResp.Body.Close()
		if upstreamResp.StatusCode != http.StatusOK && upstreamResp.StatusCode != http.StatusNotFound {
			return serveLocal("serving-stale")
		}
		return cacheAndServe(upstreamResp)
	}

	if s3c != nil {
		if err := s3c.fetch(req.Context(), hash, cacheDir); err == nil {
			// S3 cache hit
			otel.RecordCacheHit(req.Context(), up.name)
			return serveLocal("s3-hit")
		} else if !isS3NotFound(err) {
			log.Printf("[s3] fetch %s: %v", hash, err)
		}
	}

	// Local cache miss - need to fetch from upstream
	otel.RecordCacheMiss(req.Context(), up.name)
	
	log.Printf("fetching %s %s %s", up.name, req.URL.RequestURI(), hash)

	upstreamResp, err := proxyUpstream(config, up, req)
	if err != nil {
		return nil, err
	}
	defer upstreamResp.Body.Close()

	switch upstreamResp.StatusCode {
	case http.StatusOK, http.StatusNotFound:
		return cacheAndServe(upstreamResp)
	default:
		log.Printf("[%s] unexpected upstream response: %d", up.name, upstreamResp.StatusCode)
		// Record upstream error
		otel.RecordUpstreamError(req.Context(), up.name)
		return &http.Response{
			StatusCode: http.StatusBadGateway,
			Body:       http.NoBody,
		}, nil
	}
}

func getCachedItemWithFallback(config *config.Config, s3c *s3Store, up *upstream, req *http.Request) (*http.Response, error) {
	resp, err := getCachedItem(config, s3c, up, cloneRequestHead(req))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusNotFound && up.fallback != nil {
		resp.Body.Close()
		return getCachedItemWithFallback(config, s3c, up.fallback, cloneRequestHead(req))
	}

	return resp, nil
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
		log.Printf("%s %s %d %s", r.Method, r.URL.RequestURI(), rec.status, time.Since(start))
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
				w.Header().Set("WWW-Authenticate", `Basic realm="artifacts-proxy"`)
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
					slog.Info("[authMiddleware] throttled", slog.String("ip", ip))
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

func RunServer(ctx context.Context, listener net.Listener, config *config.Config) error {
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
		log.Printf("S3 storage enabled: bucket=%s region=%s", config.S3.Bucket, config.S3.Region)
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
		up := up // capture loop variable
		mux.Handle("GET "+up.path+"/", authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Add upstream to context for metrics/tracing
			ctx := WithUpstream(r.Context(), up.name)
			
			resp, err := getCachedItemWithFallback(config, s3c, up, r.WithContext(ctx))
			if err != nil {
				log.Printf("[%s] error: %v", up.name, err)
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

	// Default route should apply auth to prevent route discovery.
	mux.Handle("/", authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	})))

	// Build the middleware chain
	var handler http.Handler = mux
	
	// Add OTEL middleware if OTEL is enabled
	if config.OTEL != nil && config.OTEL.Enabled {
		// Add tracing middleware first (innermost)
		handler = otel.NewTracingMiddleware(handler)
		
		// Initialize OTEL metrics if not already initialized
		if err := otel.InitMetrics(); err != nil {
			log.Printf("Warning: failed to initialize OTEL metrics: %v", err)
		} else {
			// Add metrics middleware
			handler = otel.NewMetricsMiddleware(handler)
		}
	}
	
	handler = loggingMiddleware(handler)

	server := &http.Server{
		Handler: handler,
		BaseContext: func(listener net.Listener) context.Context {
			return ctx
		},
	}

	// Shutdown OTEL on server shutdown
	go func() {
		<-ctx.Done()
		log.Println("Shutting down OTEL providers...")
		if shutdownErr := otel.Shutdown(ctx); shutdownErr != nil {
			log.Printf("Error shutting down OTEL: %v", shutdownErr)
		}
	}()

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
