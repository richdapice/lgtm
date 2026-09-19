package jev

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAskSendsBearerAndDecodes(t *testing.T) {
	var got request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/systemone" || r.Method != http.MethodPost {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer k-1" {
			t.Errorf("auth header = %q", r.Header.Get("Authorization"))
		}
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"model":"jev-1.13.0","answers":{"q":{"type":"choice","choice":"fix","confidence":0.9,"probabilities":{"fix":0.9,"accept":0.1}}},"usage":{"input_tokens":1000000,"output_tokens":3}}`))
	}))
	defer srv.Close()
	c := &Client{Key: "k-1", BaseURL: srv.URL, Model: "jev-latest"}
	resp, err := c.Ask(context.Background(), map[string]string{"a": "b"}, map[string]Question{
		"q": {Type: "choice", Instructions: "which?", Criteria: map[string]string{"fix": "x", "accept": "y"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "jev-latest" || got.Questions["q"].Type != "choice" {
		t.Fatalf("request = %+v", got)
	}
	if a := resp.Answers["q"]; a.Choice != "fix" || a.Confidence != 0.9 || a.Probabilities["accept"] != 0.1 {
		t.Fatalf("answer = %+v", a)
	}
	if c := resp.CostUSD(); c < 0.0419 || c > 0.0421 {
		t.Fatalf("cost = %v, want a million input tokens at list price", resp.CostUSD())
	}
}

func TestAskRetriesRateLimit(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write([]byte(`{"model":"m","answers":{},"usage":{}}`))
	}))
	defer srv.Close()
	c := &Client{Key: "k", BaseURL: srv.URL, Model: "m"}
	if _, err := c.Ask(context.Background(), "s", nil); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want a retry after 429", calls)
	}
}

func TestAskBadKeyIsNotRetried(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := &Client{Key: "bad", BaseURL: srv.URL, Model: "m"}
	_, err := c.Ask(context.Background(), "s", nil)
	if err == nil || !strings.Contains(err.Error(), "TYPESAFE_API_KEY") {
		t.Fatalf("err = %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d", calls)
	}
}

func TestFromEnv(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "")
	if FromEnv() != nil {
		t.Fatal("no key must mean no client")
	}
	t.Setenv("TYPESAFE_API_KEY", "k")
	t.Setenv("TYPESAFE_BASE_URL", "")
	t.Setenv("TYPESAFE_DEFAULT_MODEL", "")
	c := FromEnv()
	if c == nil || c.BaseURL != DefaultBaseURL || c.Model != DefaultModel {
		t.Fatalf("client = %+v", c)
	}
}
