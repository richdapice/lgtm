// Package jev is a client for TypeSafe's System One endpoint. Jev answers
// typed questions about a piece of state — a probability, a choice with a
// distribution, a score on a rubric — and never generates prose. lgtm uses it
// for judgement calls that need semantic understanding but not a reasoning
// model: is this finding real, what should the author do about it.
//
// There is no SDK dependency on purpose: the API is one POST and the SDK
// would be the only new module in go.mod.
package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const (
	DefaultBaseURL = "https://api.typesafe.ai"
	DefaultModel   = "jev-latest"
	// inputUSDPerToken is Jev's list price; output tokens are free. The
	// number only feeds the ≈$ estimate on the bar and is not billing.
	inputUSDPerToken = 0.042 / 1e6
)

type Client struct {
	Key     string
	BaseURL string
	Model   string
	HTTP    *http.Client
}

// FromEnv reads TYPESAFE_API_KEY (and TYPESAFE_BASE_URL, TYPESAFE_DEFAULT_MODEL
// when set, the names the official SDKs use). No key means no client: the
// caller treats nil as "triage off" rather than an error, because a machine
// without a key still reviews fine.
func FromEnv() *Client {
	key := os.Getenv("TYPESAFE_API_KEY")
	if key == "" {
		return nil
	}
	c := &Client{Key: key, BaseURL: os.Getenv("TYPESAFE_BASE_URL"), Model: os.Getenv("TYPESAFE_DEFAULT_MODEL")}
	if c.BaseURL == "" {
		c.BaseURL = DefaultBaseURL
	}
	if c.Model == "" {
		c.Model = DefaultModel
	}
	return c
}

// Question is one judgement over the state. Type is noul, choice, or score.
// Criteria is a map of option → description for choice, a true/false map for
// noul (optional), or an ordered list of level descriptions for score.
type Question struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"`
}

// Answer is the union of the three answer shapes; the zero fields for the
// other types are simply absent from the JSON. Noul is a pointer so that a
// missing probability reads as missing, not as zero — zero is the one value
// every threshold is below.
type Answer struct {
	Type          string             `json:"type"`
	Noul          *float64           `json:"noul,omitempty"`
	Choice        string             `json:"choice"`
	Score         float64            `json:"score"`
	Confidence    float64            `json:"confidence"`
	Probabilities map[string]float64 `json:"probabilities"`
}

type Response struct {
	Model   string            `json:"model"`
	Answers map[string]Answer `json:"answers"`
	Usage   struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// CostUSD is what the request would cost at list price.
func (r Response) CostUSD() float64 { return float64(r.Usage.InputTokens) * inputUSDPerToken }

type request struct {
	Model     string              `json:"model"`
	State     any                 `json:"state"`
	Questions map[string]Question `json:"questions"`
}

// Ask sends every question over the same state in one request. The questions
// run in parallel on the model's side and cannot see each other's answers.
// 429 and 529 are retried with backoff; anything else is returned as-is.
func (c *Client) Ask(ctx context.Context, state any, questions map[string]Question) (Response, error) {
	body, err := json.Marshal(request{Model: c.Model, State: state, Questions: questions})
	if err != nil {
		return Response{}, fmt.Errorf("jev: encode request: %w", err)
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	delay := time.Second
	for attempt := 0; ; attempt++ {
		res, retry, err := c.once(ctx, hc, body)
		if err == nil || !retry || attempt == 3 {
			return res, err
		}
		select {
		case <-ctx.Done():
			return Response{}, ctx.Err()
		case <-time.After(delay):
		}
		delay *= 2
	}
}

func (c *Client) once(ctx context.Context, hc *http.Client, body []byte) (Response, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/systemone", bytes.NewReader(body))
	if err != nil {
		return Response{}, false, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return Response{}, false, fmt.Errorf("jev: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return Response{}, false, fmt.Errorf("jev: read response: %w", err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusTooManyRequests, 529:
		return Response{}, true, fmt.Errorf("jev: HTTP %d: %s", resp.StatusCode, clip(raw))
	case http.StatusUnauthorized:
		return Response{}, false, errors.New("jev: HTTP 401: TYPESAFE_API_KEY was not accepted")
	default:
		return Response{}, false, fmt.Errorf("jev: HTTP %d: %s", resp.StatusCode, clip(raw))
	}
	var out Response
	if err := json.Unmarshal(raw, &out); err != nil {
		return Response{}, false, fmt.Errorf("jev: decode response: %w", err)
	}
	return out, false, nil
}

func clip(b []byte) string {
	s := string(bytes.TrimSpace(b))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
