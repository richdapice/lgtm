package gh

import "time"

type Repo struct {
	Owner   string
	Name    string
	OpenPRs int
}

type PR struct {
	Number         int       `json:"number"`
	Title          string    `json:"title"`
	Author         Author    `json:"author"`
	IsDraft        bool      `json:"isDraft"`
	CreatedAt      time.Time `json:"createdAt"`
	ReviewDecision string    `json:"reviewDecision"`
}

type Author struct {
	Login string `json:"login"`
}

type PRDetail struct {
	Number            int        `json:"number"`
	Title             string     `json:"title"`
	Body              string     `json:"body"`
	Author            Author     `json:"author"`
	BaseRefName       string     `json:"baseRefName"`
	HeadRefName       string     `json:"headRefName"`
	HeadRefOid        string     `json:"headRefOid"`
	IsDraft           bool       `json:"isDraft"`
	Additions         int        `json:"additions"`
	Deletions         int        `json:"deletions"`
	Files             []PRFile   `json:"files"`
	StatusCheckRollup []CheckRun `json:"statusCheckRollup"`
	URL               string     `json:"url"`
}

type PRFile struct {
	Path      string `json:"path"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

type CheckRun struct {
	Name       string `json:"name"`
	Status     string `json:"status"`     // e.g. COMPLETED, IN_PROGRESS
	Conclusion string `json:"conclusion"` // e.g. SUCCESS, FAILURE ("" while running)
	State      string `json:"state"`      // StatusContext variant: SUCCESS/FAILURE/PENDING
}

// ChecksSummary reduces the rollup to passed/failed/pending counts.
func (d PRDetail) ChecksSummary() (pass, fail, pending int) {
	for _, c := range d.StatusCheckRollup {
		outcome := c.Conclusion
		if outcome == "" {
			outcome = c.State
		}
		switch outcome {
		case "SUCCESS", "NEUTRAL", "SKIPPED":
			pass++
		case "FAILURE", "ERROR", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "STARTUP_FAILURE":
			fail++
		default:
			pending++
		}
	}
	return
}
