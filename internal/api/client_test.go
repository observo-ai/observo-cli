package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	c, err := New(Options{
		BaseURL: baseURL,
		APIKey:  "test-key",
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Shorten waits so retry tests don't dominate suite runtime.
	c.InitialWait = 1 * time.Millisecond
	c.MaxWait = 4 * time.Millisecond
	return c
}

func TestNew_ValidatesRequiredFields(t *testing.T) {
	if _, err := New(Options{APIKey: "k"}); err == nil {
		t.Fatal("expected error for empty BaseURL")
	}
	if _, err := New(Options{BaseURL: "https://x"}); err == nil {
		t.Fatal("expected error for empty APIKey")
	}
	if _, err := New(Options{BaseURL: "https://x", APIKey: "k"}); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestDoJSON_SendsBearerAuthAndDecodes200(t *testing.T) {
	var gotAuth, gotCT, gotUA, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		gotUA = r.Header.Get("User-Agent")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"hello":"world"}`))
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL, APIKey: "secret-key", UserAgent: "test-ua"})
	if err != nil {
		t.Fatal(err)
	}

	var out struct {
		Hello string `json:"hello"`
	}
	body := map[string]any{"x": 1}
	if err := c.DoJSON(context.Background(), http.MethodPost, "/api/things", body, &out); err != nil {
		t.Fatalf("DoJSON: %v", err)
	}

	if gotAuth != "Bearer secret-key" {
		t.Errorf("auth header: got %q", gotAuth)
	}
	if gotCT != "application/json" {
		t.Errorf("content-type: got %q", gotCT)
	}
	if gotUA != "test-ua" {
		t.Errorf("user-agent: got %q", gotUA)
	}
	if gotBody != `{"x":1}` {
		t.Errorf("body: got %q", gotBody)
	}
	if out.Hello != "world" {
		t.Errorf("decoded out: got %+v", out)
	}
}

func TestDoJSON_HTTPErrorOn4xx_NoRetry(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad input"}`))
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)
	err := c.DoJSON(context.Background(), http.MethodGet, "/x", nil, nil)
	var herr *HTTPError
	if !errors.As(err, &herr) {
		t.Fatalf("expected *HTTPError, got %T: %v", err, err)
	}
	if herr.StatusCode != http.StatusBadRequest {
		t.Errorf("status: got %d", herr.StatusCode)
	}
	if calls.Load() != 1 {
		t.Errorf("4xx must NOT retry; calls=%d", calls.Load())
	}
	if !strings.Contains(herr.Body, "bad input") {
		t.Errorf("body not propagated: %q", herr.Body)
	}
}

func TestDoJSON_RetriesOn5xxThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := calls.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("oops"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)
	var out struct {
		OK bool `json:"ok"`
	}
	if err := c.DoJSON(context.Background(), http.MethodGet, "/x", nil, &out); err != nil {
		t.Fatalf("DoJSON: %v", err)
	}
	if !out.OK {
		t.Errorf("decoded out: %+v", out)
	}
	if calls.Load() != 3 {
		t.Errorf("expected 3 calls (2 fails + 1 success), got %d", calls.Load())
	}
}

func TestDoJSON_GivesUpAfterMaxRetries(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)
	err := c.DoJSON(context.Background(), http.MethodGet, "/x", nil, nil)
	if err == nil {
		t.Fatal("expected error after exhausted retries")
	}
	if calls.Load() != int32(c.MaxRetries+1) {
		t.Errorf("expected %d calls (initial + retries), got %d", c.MaxRetries+1, calls.Load())
	}
	if !strings.Contains(err.Error(), "after 3 retries") {
		t.Errorf("wrap message: %v", err)
	}
}

func TestDoJSON_RetriesOn429And408(t *testing.T) {
	for _, code := range []int{http.StatusTooManyRequests, http.StatusRequestTimeout} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if calls.Add(1) == 1 {
					w.WriteHeader(code)
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()

			c := newClient(t, srv.URL)
			if err := c.DoJSON(context.Background(), http.MethodGet, "/x", nil, nil); err != nil {
				t.Fatalf("DoJSON: %v", err)
			}
			if calls.Load() != 2 {
				t.Errorf("expected retry, got calls=%d", calls.Load())
			}
		})
	}
}

func TestDoJSON_ContextCancelStopsRetries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)
	c.InitialWait = 50 * time.Millisecond // long enough to land inside the select

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	err := c.DoJSON(ctx, http.MethodGet, "/x", nil, nil)
	if err == nil {
		t.Fatal("expected error from cancelled ctx")
	}
	if !errors.Is(err, errCtxCancelled) {
		t.Errorf("expected errCtxCancelled, got %v", err)
	}
}

func TestDoJSON_BodyTruncation(t *testing.T) {
	big := strings.Repeat("A", BodyMaxLen+500)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(big))
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)
	err := c.DoJSON(context.Background(), http.MethodGet, "/x", nil, nil)
	var herr *HTTPError
	if !errors.As(err, &herr) {
		t.Fatalf("expected *HTTPError, got %T: %v", err, err)
	}
	if len(herr.Body) > BodyMaxLen+len("…") {
		t.Errorf("body not truncated to %d (got %d)", BodyMaxLen, len(herr.Body))
	}
	if !strings.HasSuffix(herr.Body, "…") {
		t.Errorf("expected ellipsis suffix on truncated body")
	}
}

// OB-860: a SUCCESSFUL body bigger than the error-quote limit has to decode in
// full. The test above only ever proved that an ERROR body is shortened, and
// the two limits used to be the same number — so every 2xx over 2 KB was read
// through the same limiter and unmarshalled from the truncated bytes. Nothing
// in this package failed; `observo plan resolve` did, against any project with
// more than a couple of kilobytes of plans.
func TestDoJSON_DecodesABodyLargerThanTheErrorQuoteLimit(t *testing.T) {
	// Shaped like the response that actually broke: a list whose rows carry
	// long prose descriptions. The padding sits INSIDE a string value, so a
	// truncated read cuts mid-token and cannot parse.
	type row struct {
		Key         string `json:"plan_key"`
		Description string `json:"description"`
	}
	type payload struct {
		Plans []row `json:"plans"`
	}
	want := payload{Plans: []row{
		{Key: "REGR-MAIN-CI", Description: strings.Repeat("x", BodyMaxLen*2)},
		{Key: "smoke", Description: "short"},
	}}
	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	if len(encoded) <= BodyMaxLen {
		t.Fatalf("fixture must exceed BodyMaxLen to prove anything (got %d)", len(encoded))
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(encoded)
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)
	var got payload
	if err := c.DoJSON(context.Background(), http.MethodGet, "/x", nil, &got); err != nil {
		t.Fatalf("DoJSON: %v", err)
	}
	if len(got.Plans) != len(want.Plans) {
		t.Fatalf("got %d plans, want %d", len(got.Plans), len(want.Plans))
	}
	// The LAST row is the one a truncated read loses, and the long value is
	// what a mid-token cut destroys — assert both, not just the count.
	if got.Plans[1].Key != "smoke" {
		t.Errorf("last row lost: got %q", got.Plans[1].Key)
	}
	if got.Plans[0].Description != want.Plans[0].Description {
		t.Errorf("long value did not survive: got %d chars, want %d",
			len(got.Plans[0].Description), len(want.Plans[0].Description))
	}
}

// The other half of the same rule: the read is still bounded, and a body over
// that bound is refused rather than half-decoded.
func TestDoJSON_RefusesABodyOverTheReadLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"plans":[{"plan_key":"` + strings.Repeat("y", ResponseReadLimit) + `"}]}`))
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)
	var out map[string]any
	err := c.DoJSON(context.Background(), http.MethodGet, "/x", nil, &out)
	if err == nil {
		t.Fatal("expected a refusal for an oversized body")
	}
	if !strings.Contains(err.Error(), "larger than") {
		t.Errorf("the error should say the body was too large, got %v", err)
	}
}

func TestDoJSON_NilOutDiscardsResponseBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ignored":"yes"}`))
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)
	if err := c.DoJSON(context.Background(), http.MethodGet, "/x", nil, nil); err != nil {
		t.Fatalf("DoJSON: %v", err)
	}
}

func TestDoJSON_VerboseLogsToInjectedStderr(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL, APIKey: "k", Verbose: true})
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	prev := stderr
	stderr = func() io.Writer { return &buf }
	defer func() { stderr = prev }()

	if err := c.DoJSON(context.Background(), http.MethodGet, "/x", nil, nil); err != nil {
		t.Fatalf("DoJSON: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "→ GET") || !strings.Contains(out, "← 200") {
		t.Errorf("verbose log missing expected lines:\n%s", out)
	}
}

// Round-trip a real value through the encoder/decoder via the round-trip
// test handler — catches accidental field-name mismatches.
func TestDoJSON_RoundTripsTypedPayload(t *testing.T) {
	type payload struct {
		Name string `json:"name"`
		N    int    `json:"n"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got payload
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload{Name: got.Name + "-echo", N: got.N * 2})
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)
	var out payload
	if err := c.DoJSON(context.Background(), http.MethodPost, "/echo", payload{Name: "x", N: 21}, &out); err != nil {
		t.Fatalf("DoJSON: %v", err)
	}
	if out.Name != "x-echo" || out.N != 42 {
		t.Errorf("round-trip: got %+v", out)
	}
}
