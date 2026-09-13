package apiclient

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/pinchtab/pinchtab/internal/api/types"
)

// request describes a single API call. body is a JSON payload (nil = no body;
// Content-Type is set only when body is non-nil). headers are extra per-call
// headers applied after the standard client headers.
type request struct {
	method  string
	url     string
	body    map[string]any
	headers map[string]string
	base    string
	vocab   *vocabCapture
}

type vocabCapture struct {
	implicit bool
}

type RequestOption func(*request)

func CaptureVocab(implicit bool) RequestOption {
	return func(r *request) { r.vocab = &vocabCapture{implicit: implicit} }
}

func newRequest(method, base, url string, body map[string]any, headers map[string]string, opts []RequestOption) request {
	r := request{method: method, url: url, body: body, headers: headers, base: base}
	for _, opt := range opts {
		opt(&r)
	}
	return r
}

func buildURL(base, path string, params url.Values) string {
	u := base + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	return u
}

// doRequest builds and executes the request and reads the body. It does NOT
// interpret the status code or print anything — callers apply their own
// error/render policy.
func doRequest(client *http.Client, token string, r request) (int, []byte, error) {
	var bodyReader io.Reader
	if r.body != nil {
		data, err := json.Marshal(r.body)
		if err != nil {
			return 0, nil, fmt.Errorf("encode request body for %s: %w", r.url, err)
		}
		bodyReader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(r.method, r.url, bodyReader)
	if err != nil {
		return 0, nil, fmt.Errorf("build request: %w", err)
	}
	if r.body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	setClientHeaders(req, token)
	for key, value := range r.headers {
		req.Header.Set(key, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	// A short read is a failed request, never a body: a connection dropping
	// mid-response would otherwise reach the caller as a fragment carrying its
	// status, and the CLI would parse or print the fragment as the answer.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("read response from %s: %w", r.url, err)
	}
	if r.vocab != nil && resp.StatusCode < http.StatusBadRequest {
		storeVocabToken(r.base, resp.Header.Get(vocabTabIDHeader), resp.Header.Get(vocabHeader), r.vocab.implicit)
	}
	return resp.StatusCode, body, nil
}

func setClientHeaders(req *http.Request, token string) {
	req.Header.Set(types.HeaderSource, "client")
	if token == "" {
		return
	}
	if strings.HasPrefix(token, "ses_") {
		req.Header.Set("Authorization", "Session "+token)
	} else {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}
