package ticket

// Criteria keeps a goal ticket's checked brief, proof and pending revision.
// A changed criterion clears all proof unless the planner explicitly carries it.
type Criteria struct {
	Brief       int    `json:"brief"`
	AC          int    `json:"ac"`
	Disposition string `json:"disposition"`
	Request     string `json:"request,omitempty"`
	Checkpoint  string `json:"checkpoint,omitempty"`
	// Direct is the build head QA may assess without a reviewer. A rereview clears it.
	Direct            string `json:"direct,omitempty"`
	Build, Review, QA string `json:",omitempty"`
	Merge             string `json:"merge,omitempty"`
}
