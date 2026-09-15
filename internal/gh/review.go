package gh

import (
	"context"
	"encoding/json"
	"fmt"
)

// ReviewComment is one inline comment in a review submission, using the
// modern line/side addressing (not legacy position).
type ReviewComment struct {
	Path      string `json:"path"`
	Line      int    `json:"line"`
	Side      string `json:"side"` // RIGHT (new file) or LEFT (old file)
	StartLine int    `json:"start_line,omitempty"`
	StartSide string `json:"start_side,omitempty"`
	Body      string `json:"body"`
}

type ReviewPayload struct {
	Body     string          `json:"body"`
	Event    string          `json:"event"` // APPROVE | REQUEST_CHANGES | COMMENT
	Comments []ReviewComment `json:"comments,omitempty"`
}

// SubmitReview posts a review; commit_id is omitted so GitHub anchors to the
// current head. Callers should verify the head hasn't moved first.
func (c Client) SubmitReview(ctx context.Context, owner, repo string, number int, p ReviewPayload) error {
	payload, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = c.run(ctx, payload,
		"api", fmt.Sprintf("repos/%s/%s/pulls/%d/reviews", owner, repo, number),
		"-X", "POST", "--input", "-")
	return err
}
