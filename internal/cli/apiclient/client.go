package apiclient

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	vocabHeader      = "X-PinchTab-Vocab"
	vocabTabIDHeader = "X-PinchTab-Tab-Id"
	// VocabTabHeader tells the server which tab the echoed token belongs to, so it
	// enforces the epoch check only when the action resolves that same tab and
	// ignores a token left over from a tab the current pointer has since moved off.
	VocabTabHeader  = "X-PinchTab-Vocab-Tab"
	vocabStoreLimit = 16
)

// vocabEntry pairs a snapshot's vocabulary token with the tab the server
// actually resolved it for, so a later action echoes the token only when it
// targets that same tab.
type vocabEntry struct {
	TabID string `json:"tabId"`
	Token string `json:"token"`
}

// vocabStore is one server's token records plus the tab the last implicit
// snapshot resolved, so an implicit action can echo that tab's token and tag it.
type vocabStore struct {
	Current string       `json:"current"`
	Entries []vocabEntry `json:"entries"`
}

// DoGetCapturingVocab performs a GET like DoGet and, on success, persists the
// response's vocabulary token keyed by the tab the server resolved (the
// X-PinchTab-Tab-Id header), not by how the caller spelled --tab. When the
// snapshot was implicit (no --tab), the resolved tab also becomes the store's
// current pointer, so a later implicit action echoes that tab's token. The token
// is delivered as a response header so it survives every snapshot format,
// including the compact text the CLI defaults to.
func DoGetCapturingVocab(client *http.Client, base, token, path string, params url.Values, implicit bool) map[string]any {
	var headers http.Header
	r := request{method: "GET", url: buildURL(base, path, params), respHeaders: &headers}
	status, body := mustRequest(client, token, r)
	exitOnAPIError(r, status, body)
	storeVocabToken(base, headers.Get(vocabTabIDHeader), headers.Get(vocabHeader), implicit)
	return printAndDecode(body)
}

// VocabForAction returns the tab id to tag and the token to echo for an action.
// An explicit --tab X uses X's own record; an implicit action uses the current
// pointer's tab. The returned tab is sent as VocabTabHeader so the server can
// ignore the token when the action resolves a different tab.
func VocabForAction(base, requestedTab string) (vocabTab, token string) {
	store := loadVocabStore(base)
	tab := requestedTab
	if tab == "" {
		tab = store.Current
	}
	if tab == "" {
		return "", ""
	}
	for _, e := range store.Entries {
		if e.TabID == tab {
			return tab, e.Token
		}
	}
	return tab, ""
}

// VocabTokenFor returns the token stored for a resolved tab id, or "".
func VocabTokenFor(base, tabID string) string {
	if tabID == "" {
		return ""
	}
	_, token := VocabForAction(base, tabID)
	return token
}

func storeVocabToken(base, tabID, token string, implicit bool) {
	if tabID == "" || token == "" {
		return
	}
	store := loadVocabStore(base)
	kept := store.Entries[:0]
	for _, e := range store.Entries {
		if e.TabID != tabID {
			kept = append(kept, e)
		}
	}
	kept = append(kept, vocabEntry{TabID: tabID, Token: token})
	if len(kept) > vocabStoreLimit {
		kept = kept[len(kept)-vocabStoreLimit:]
	}
	store.Entries = kept
	if implicit {
		store.Current = tabID
	}
	writeVocabStore(base, store)
}

func loadVocabStore(base string) vocabStore {
	data, err := os.ReadFile(vocabStorePath(base))
	if err != nil {
		return vocabStore{}
	}
	var store vocabStore
	if err := json.Unmarshal(data, &store); err != nil {
		return vocabStore{}
	}
	return store
}

func writeVocabStore(base string, store vocabStore) {
	path := vocabStorePath(base)
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	data, err := json.Marshal(store)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0600)
}

func vocabStorePath(base string) string {
	dir := os.Getenv("XDG_STATE_HOME")
	if dir != "" {
		dir += "/pinchtab"
	} else if home, err := os.UserHomeDir(); err == nil {
		dir = home + "/.local/state/pinchtab"
	} else {
		dir = "/tmp/pinchtab"
	}
	return filepath.Join(dir, "vocab-"+fileSlug(base))
}

func fileSlug(s string) string {
	s = strings.TrimPrefix(strings.TrimPrefix(s, "http://"), "https://")
	s = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-':
			return r
		default:
			return '-'
		}
	}, strings.Trim(s, "/"))
	if s == "" {
		return "default"
	}
	return s
}

// doAndRender runs the request with the standard fatal-on-transport-error +
// exit-on-HTTP-error policy, then pretty-prints and decodes the body.
func doAndRender(client *http.Client, token string, r request) map[string]any {
	status, body := mustRequest(client, token, r)
	exitOnAPIError(r, status, body)
	return printAndDecode(body)
}

func DoGet(client *http.Client, base, token, path string, params url.Values) map[string]any {
	return doAndRender(client, token, request{method: "GET", url: buildURL(base, path, params)})
}

func DoGetRaw(client *http.Client, base, token, path string, params url.Values) []byte {
	r := request{method: "GET", url: buildURL(base, path, params)}
	status, body := mustRequest(client, token, r)
	exitOnAPIError(r, status, body)
	return body
}

// DoGetRawAndPrint fetches and prints the raw response body (for --snap flag).
// Best-effort: it reports errors to stderr but does not exit.
func DoGetRawAndPrint(client *http.Client, base, token, pathWithQuery string) {
	status, body, err := doRequest(client, token, request{method: "GET", url: base + pathWithQuery})
	if err != nil {
		fmt.Fprintf(os.Stderr, "snapshot failed: %v\n", err)
		return
	}
	if status >= 400 {
		fmt.Fprintf(os.Stderr, "snapshot error %d: %s\n", status, string(body))
		return
	}
	fmt.Println(string(body))
}

func DoPost(client *http.Client, base, token, path string, body map[string]any) map[string]any {
	return DoPostWithHeaders(client, base, token, path, body, nil)
}

// DoPostQuiet is like DoPost but does not print the response body. Callers are
// responsible for rendering whatever output is appropriate (e.g. a single
// field for machine-friendly piping).
func DoPostQuiet(client *http.Client, base, token, path string, body map[string]any) map[string]any {
	return DoPostQuietWithHeaders(client, base, token, path, body, nil)
}

// DoPostRaw sends a POST and returns the raw response body without printing.
// Exits on HTTP errors.
func DoPostRaw(client *http.Client, base, token, path string, body map[string]any) []byte {
	statusCode, respBody, _ := doPostQuietWithStatus(client, base, token, path, body, nil)
	exitOnAPIError(request{method: "POST", url: base + path, body: body}, statusCode, respBody)
	return respBody
}

// DoPostRawE sends a POST and returns an error instead of terminating the
// process. Long-running commands use it when they need to release resources
// before reporting a request failure.
func DoPostRawE(client *http.Client, base, token, path string, body map[string]any) ([]byte, error) {
	r := request{method: "POST", url: base + path, body: body}
	statusCode, respBody, err := doRequest(client, token, r)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	if statusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("%s", strings.TrimSpace(renderAPIError(r, statusCode, respBody)))
	}
	return respBody, nil
}

// DoGetRawE sends a GET and returns an error instead of terminating the
// process. See DoPostRawE.
func DoGetRawE(client *http.Client, base, token, path string, params url.Values) ([]byte, error) {
	r := request{method: "GET", url: buildURL(base, path, params)}
	statusCode, respBody, err := doRequest(client, token, r)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	if statusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("%s", strings.TrimSpace(renderAPIError(r, statusCode, respBody)))
	}
	return respBody, nil
}

func DoPostQuietWithStatus(client *http.Client, base, token, path string, body map[string]any) (int, []byte, map[string]any) {
	return doPostQuietWithStatus(client, base, token, path, body, nil)
}

// DoPostQuietWithHeaders is like DoPostQuiet but allows custom headers.
func DoPostQuietWithHeaders(client *http.Client, base, token, path string, body map[string]any, headers map[string]string) map[string]any {
	statusCode, respBody, result := doPostQuietWithStatus(client, base, token, path, body, headers)
	exitOnAPIError(request{method: "POST", url: base + path, body: body, headers: headers}, statusCode, respBody)
	return result
}

func doPostQuietWithStatus(client *http.Client, base, token, path string, body map[string]any, headers map[string]string) (int, []byte, map[string]any) {
	status, respBody := mustRequest(client, token, request{method: "POST", url: base + path, body: body, headers: headers})

	var result map[string]any
	if status < 400 {
		// Object responses populate result; array/scalar responses leave it nil.
		// Callers that need a map should branch on result == nil.
		_ = json.Unmarshal(respBody, &result)
	}
	return status, respBody, result
}

func DoPostWithHeaders(client *http.Client, base, token, path string, body map[string]any, headers map[string]string) map[string]any {
	return doAndRender(client, token, request{method: "POST", url: base + path, body: body, headers: headers})
}

// DoDelete sends a DELETE request with an optional JSON body (e.g. for ?name= query params, pass nil body and handle params in path).
func DoDelete(client *http.Client, base, token, path string, params url.Values) map[string]any {
	return doAndRender(client, token, request{method: "DELETE", url: buildURL(base, path, params)})
}

// DoDeleteJSON sends a DELETE request with a JSON body.
func DoDeleteJSON(client *http.Client, base, token, path string, body map[string]any) map[string]any {
	return doAndRender(client, token, request{method: "DELETE", url: base + path, body: body})
}

// ResolveInstanceBase fetches the named instance from the orchestrator and returns
// a base URL pointing directly at that instance's API port.
func ResolveInstanceBase(orchBase, token, instanceID, bind string) string {
	c := &http.Client{Timeout: 10 * time.Second}
	body := DoGetRaw(c, orchBase, token, fmt.Sprintf("/instances/%s", instanceID), nil)

	var inst struct {
		Port string `json:"port"`
	}
	if err := json.Unmarshal(body, &inst); err != nil {
		fatal("failed to parse instance %q: %v", instanceID, err)
	}
	if inst.Port == "" {
		fatal("instance %q has no port assigned (is it still starting?)", instanceID)
	}
	return fmt.Sprintf("http://%s:%s", bind, inst.Port)
}
