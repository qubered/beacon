package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/qubered/beacon/internal/agent/egress"
	"github.com/qubered/beacon/internal/engine/frame"
	"github.com/qubered/beacon/internal/engine/runtime"
	"github.com/qubered/beacon/internal/flow/graph"
	"github.com/qubered/beacon/internal/flow/types"
	"github.com/qubered/beacon/internal/nodes/registry"
)

// HTTPRequestConfig configures transport.http_request.
type HTTPRequestConfig struct {
	Method  string            `json:"method,omitempty"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    []byte            `json:"body,omitempty"`

	TLS *TLSConfig `json:"tls,omitempty"`

	// MaxRedirects of 0 means redirects are not followed at all, which is the
	// safe default for a monitoring check: a 302 is usually itself the signal.
	MaxRedirects int `json:"max_redirects,omitempty"`

	// MaxBodyBytes caps the response body read. Zero uses defaultMaxBody.
	MaxBodyBytes int64 `json:"max_body_bytes,omitempty"`
}

const defaultMaxBody = 8 << 20

type httpRequestNode struct{ cfg HTTPRequestConfig }

func newHTTPRequest(n graph.Node) (runtime.Executable, error) {
	var cfg HTTPRequestConfig
	if len(n.Config) > 0 {
		if err := json.Unmarshal(n.Config, &cfg); err != nil {
			return nil, fmt.Errorf("transport.http_request: invalid config: %w", err)
		}
	}
	if cfg.URL == "" {
		return nil, fmt.Errorf("transport.http_request needs a URL")
	}
	if _, err := parseHTTPURL(cfg.URL); err != nil {
		return nil, fmt.Errorf("transport.http_request: %w", err)
	}
	if cfg.Method == "" {
		cfg.Method = http.MethodGet
	}
	return &httpRequestNode{cfg: cfg}, nil
}

func (h *httpRequestNode) Execute(ctx context.Context, rc *runtime.RunContext, in runtime.Inputs) (runtime.Outputs, error) {
	dialer, err := egress.DialerFrom(ctx)
	if err != nil {
		return nil, frame.Fail(frame.ClassInternal, "%s", err)
	}

	body := h.cfg.Body
	var bodyFrame *frame.Frame
	if f, ok := in["body"]; ok {
		b, ok := f.Value.([]byte)
		if !ok {
			return nil, frame.Fail(frame.ClassInternal, "body input is not bytes (got %T)", f.Value)
		}
		body = b
		bodyFrame = &f
	}

	client, err := h.client(dialer)
	if err != nil {
		return nil, frame.Fail(frame.ClassInternal, "%s", err)
	}

	req, err := http.NewRequestWithContext(ctx, h.cfg.Method, h.cfg.URL, bodyReader(body))
	if err != nil {
		return nil, frame.Fail(frame.ClassInternal, "building request: %v", err)
	}
	for k, v := range h.cfg.Headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fail(unwrapURLError(err), "requesting %s: %v", h.cfg.URL, unwrapURLError(err))
	}
	defer resp.Body.Close()

	limit := h.cfg.MaxBodyBytes
	if limit <= 0 {
		limit = defaultMaxBody
	}
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, fail(err, "reading response body from %s: %v", h.cfg.URL, err)
	}

	headers := frame.Record{}
	for k, v := range resp.Header {
		headers[k] = strings.Join(v, ", ")
	}

	// The body is bytes, not string (invariant I1). An HTTP response declaring
	// a charset is still a claim by the device, and a device that lies about
	// its encoding should produce a visible decode failure rather than silent
	// mojibake three nodes downstream.
	out := runtime.Outputs{
		"status":  {Type: types.Int(), Value: int64(resp.StatusCode)},
		"headers": {Type: types.Record(), Value: headers},
	}
	if bodyFrame != nil {
		out["body"] = bodyFrame.Derive(types.Bytes(), respBody)
	} else {
		out["body"] = frame.Frame{Type: types.Bytes(), Value: respBody}
	}
	return out, nil
}

// client builds an HTTP client whose every connection is checked and pinned.
//
// This is the subtle half of the egress control. An http.Transport resolves
// the hostname itself, inside its own dial, which would re-resolve after our
// check and reopen DNS rebinding. So DialContext here does the resolve, the
// check and the connect as one step and hands back a socket to a pinned
// address — the hostname never reaches a second resolver.
//
// The redirect policy checks each hop the same way. A redirect to a denied
// host is a hard failure and not a follow (spec §16): following it would let
// any reachable server steer an agent at the metadata address.
func (h *httpRequestNode) client(dialer *egress.Dialer) (*http.Client, error) {
	tr := &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, portStr, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			port, err := strconv.Atoi(portStr)
			if err != nil {
				return nil, fmt.Errorf("invalid port %q", portStr)
			}
			return dialer.Dial(ctx, "tcp", host, port)
		},
	}
	if h.cfg.TLS != nil {
		tc, err := h.cfg.TLS.clientConfig("")
		if err != nil {
			return nil, err
		}
		// ServerName is left to the transport so SNI follows a redirect
		// correctly; an explicit override in config still wins.
		if h.cfg.TLS.ServerName != "" {
			tc.ServerName = h.cfg.TLS.ServerName
		} else {
			tc.ServerName = ""
		}
		tr.TLSClientConfig = tc

		if h.cfg.TLS.Fingerprint != "" {
			pinned := *h.cfg.TLS
			tr.DialTLSContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, portStr, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}
				port, err := strconv.Atoi(portStr)
				if err != nil {
					return nil, fmt.Errorf("invalid port %q", portStr)
				}
				conn, err := dialer.Dial(ctx, "tcp", host, port)
				if err != nil {
					return nil, err
				}
				tlsConn, err := wrapTLS(ctx, conn, host, pinned)
				if err != nil {
					conn.Close()
					return nil, err
				}
				return tlsConn, nil
			}
		}
	}

	maxRedirects := h.cfg.MaxRedirects
	return &http.Client{
		Transport: tr,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > maxRedirects {
				// Report the 3xx rather than failing. For a monitoring check
				// the redirect usually *is* the signal, and an Assert on the
				// status code says so far more clearly than an execution
				// error — the node reports, the flow decides.
				return http.ErrUseLastResponse
			}
			host, port, err := hostPort(req.URL)
			if err != nil {
				return err
			}
			// Check the redirect target before the client dials it. Returning
			// an error here aborts the request, which is the hard failure the
			// spec requires — not a silent stop that would look like a
			// successful response with an empty body.
			if _, err := dialer.Allow(req.Context(), host, port, egress.ProtoTCP); err != nil {
				return fmt.Errorf("redirect to %s: %w", req.URL.Redacted(), err)
			}
			return nil
		},
	}, nil
}

func bodyReader(b []byte) io.Reader {
	if len(b) == 0 {
		return nil
	}
	return bytes.NewReader(b)
}

func parseHTTPURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid URL %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("URL scheme must be http or https (got %q)", u.Scheme)
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("URL %q has no host", raw)
	}
	return u, nil
}

func hostPort(u *url.URL) (string, int, error) {
	host := u.Hostname()
	if p := u.Port(); p != "" {
		port, err := strconv.Atoi(p)
		if err != nil {
			return "", 0, fmt.Errorf("invalid port in %q", u.Redacted())
		}
		return host, port, nil
	}
	if u.Scheme == "https" {
		return host, 443, nil
	}
	return host, 80, nil
}

// unwrapURLError digs the transport error out of the *url.Error the client
// wraps everything in, so classify sees the syscall or DNS error rather than
// the wrapper.
func unwrapURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

func init() {
	registry.MustRegister(registry.Descriptor{
		Type:                "transport.http_request",
		Title:               "HTTP Request",
		Summary:             "Send an HTTP request and return the status, headers and raw body.",
		Category:            "Transport",
		Tier:                registry.Tier1,
		Synonyms:            []string{"http", "https", "rest", "api", "web", "get", "post", "url"},
		ConfigSchemaVersion: 1,
		Inputs:              []registry.Port{{Name: "body", Type: types.Bytes(), Optional: true}},
		Outputs: []registry.Port{
			{Name: "body", Type: types.Bytes()},
			{Name: "status", Type: types.Int()},
			{Name: "headers", Type: types.Record()},
		},
		PerformsEgress: true,
		New:            newHTTPRequest,
	})
}
