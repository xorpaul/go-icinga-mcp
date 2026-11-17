package icinga

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/xorpaul/go-icinga-mcp/internal/config"
)

type Client struct {
	cfg     *config.Settings
	http    *http.Client
	baseURL string
}

func NewClient(cfg *config.Settings) (*Client, error) {
	tlsConf := &tls.Config{InsecureSkipVerify: !cfg.VerifyTLS}

	if cfg.CABundle != "" {
		pem, err := os.ReadFile(cfg.CABundle)
		if err != nil {
			return nil, fmt.Errorf("read CA bundle %q: %w", cfg.CABundle, err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("CA bundle %q: no certificates parsed", cfg.CABundle)
		}
		tlsConf.RootCAs = pool
	}

	if cfg.ClientCert != "" && cfg.ClientKey != "" {
		cert, err := tls.LoadX509KeyPair(cfg.ClientCert, cfg.ClientKey)
		if err != nil {
			return nil, fmt.Errorf("load client cert/key: %w", err)
		}
		// Always present this cert when the server requests one. Go's default
		// cert-selection (via tlsConf.Certificates) inspects the server's
		// CertificateRequest acceptable-CA list and silently sends nothing if
		// the cert's issuer is missing — which results in opaque "certificate
		// required" errors against servers that don't advertise their CA list.
		tlsConf.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			return &cert, nil
		}
	}

	transport := &http.Transport{
		TLSClientConfig:       tlsConf,
		ForceAttemptHTTP2:     true,
		ResponseHeaderTimeout: time.Duration(cfg.RequestTimeoutSec * float64(time.Second)),
		DisableCompression:    false,
	}

	return &Client{
		cfg: cfg,
		http: &http.Client{
			Transport: transport,
			Timeout:   time.Duration(cfg.RequestTimeoutSec * float64(time.Second)),
			// Treat 3xx as a real response so we can flag upstream auth-redirects as errors.
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		baseURL: strings.TrimRight(cfg.BaseURL, "/") + "/",
	}, nil
}

// encodeQuery percent-encodes params with one quirk: spaces become %20 (not
// '+'), which makes Icinga 2 DSL filter strings (e.g. `host.state != 0`) round
// trip cleanly through both the URL and Icinga's filter parser.
func encodeQuery(params map[string]string) string {
	if len(params) == 0 {
		return ""
	}
	const safe = "!$'()*,-._~"
	pairs := make([]string, 0, len(params))
	for k, v := range params {
		if v == "" {
			continue
		}
		pairs = append(pairs, percentEncode(k, safe)+"="+percentEncode(v, safe))
	}
	return strings.Join(pairs, "&")
}

// percentEncode mirrors Python's urllib.parse.quote(s, safe=...) — encoding
// every byte except letters, digits, and the explicit safe set.
func percentEncode(s, safe string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case 'A' <= c && c <= 'Z', 'a' <= c && c <= 'z', '0' <= c && c <= '9':
			b.WriteByte(c)
		case strings.IndexByte(safe, c) >= 0:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

type RequestOpts struct {
	Method     string
	Path       string
	Params     map[string]string
	FormFields map[string]string
	JSONBody   any
}

func (c *Client) Request(ctx context.Context, opts RequestOpts) (*http.Response, []byte, error) {
	if opts.Method == "" {
		opts.Method = http.MethodGet
	}
	full := c.baseURL + strings.TrimLeft(opts.Path, "/")
	if q := encodeQuery(opts.Params); q != "" {
		full += "?" + q
	}

	if opts.FormFields != nil && opts.JSONBody != nil {
		return nil, nil, errors.New("FormFields and JSONBody are mutually exclusive")
	}

	var (
		resp *http.Response
		body []byte
		last error
	)

	maxAttempts := c.cfg.MaxRetries
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	backoff := c.cfg.RetryBackoffSec

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		var bodyR io.Reader
		var contentType string
		if opts.JSONBody != nil {
			b, err := json.Marshal(opts.JSONBody)
			if err != nil {
				return nil, nil, fmt.Errorf("marshal json body: %w", err)
			}
			bodyR = bytes.NewReader(b)
			contentType = "application/json"
		} else if opts.FormFields != nil {
			form := url.Values{}
			for k, v := range opts.FormFields {
				form.Set(k, v)
			}
			bodyR = strings.NewReader(form.Encode())
			contentType = "application/x-www-form-urlencoded"
		}

		req, err := http.NewRequestWithContext(ctx, opts.Method, full, bodyR)
		if err != nil {
			return nil, nil, err
		}
		req.Header.Set("Accept", "application/json")
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		if c.cfg.Username != "" {
			req.SetBasicAuth(c.cfg.Username, c.cfg.Password)
		}

		resp, last = c.http.Do(req)
		if last != nil {
			if attempt < maxAttempts {
				sleepBackoff(ctx, backoff, attempt)
				continue
			}
			return nil, nil, last
		}

		body, last = io.ReadAll(resp.Body)
		resp.Body.Close()
		if last != nil {
			if attempt < maxAttempts {
				sleepBackoff(ctx, backoff, attempt)
				continue
			}
			return nil, nil, last
		}

		// 3xx from upstream is almost always an auth-redirect masquerading as success.
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			return resp, body, fmt.Errorf("upstream redirect %d to %q", resp.StatusCode, resp.Header.Get("Location"))
		}
		// Retry transient 5xx; surface 4xx immediately.
		if resp.StatusCode >= 500 && attempt < maxAttempts {
			sleepBackoff(ctx, backoff, attempt)
			continue
		}
		return resp, body, nil
	}
	return resp, body, last
}

func sleepBackoff(ctx context.Context, base float64, attempt int) {
	d := base * math.Pow(2, float64(attempt-1))
	if d > 5 {
		d = 5
	}
	jitter := rand.Float64() * d
	t := time.NewTimer(time.Duration((d + jitter) * float64(time.Second)))
	select {
	case <-ctx.Done():
		t.Stop()
	case <-t.C:
	}
}

func (c *Client) GetJSON(ctx context.Context, path string, params map[string]string) (any, error) {
	resp, body, err := c.Request(ctx, RequestOpts{Method: http.MethodGet, Path: path, Params: params})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("upstream %d %s: %s", resp.StatusCode, resp.Status, snippet(body))
	}
	if len(body) == 0 {
		return nil, nil
	}
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, fmt.Errorf("decode json: %w (body: %s)", err, snippet(body))
	}
	return v, nil
}

func (c *Client) PostJSON(ctx context.Context, path string, jsonBody any) (any, error) {
	resp, body, err := c.Request(ctx, RequestOpts{Method: http.MethodPost, Path: path, JSONBody: jsonBody})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("upstream %d %s: %s", resp.StatusCode, resp.Status, snippet(body))
	}
	if len(body) == 0 {
		return nil, nil
	}
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, fmt.Errorf("decode json: %w (body: %s)", err, snippet(body))
	}
	return v, nil
}

func snippet(b []byte) string {
	const max = 512
	if len(b) > max {
		return string(b[:max]) + "…"
	}
	return string(b)
}

// UnwrapResults flattens an Icinga 2 REST envelope of the form
//
//	{ "results": [ {"name":"…","type":"Host","attrs":{…},"joins":{},"meta":{}} ] }
//
// into a plain []any of the per-object `attrs` maps, which is what the
// projection layer expects. If `attrs` is missing on an entry the entry is
// kept verbatim. Returns nil if the input isn't a results envelope.
func UnwrapResults(v any) []any {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	raw, ok := m["results"].([]any)
	if !ok {
		return nil
	}
	out := make([]any, 0, len(raw))
	for _, it := range raw {
		item, ok := it.(map[string]any)
		if !ok {
			continue
		}
		if attrs, ok := item["attrs"].(map[string]any); ok {
			out = append(out, attrs)
			continue
		}
		out = append(out, item)
	}
	return out
}
