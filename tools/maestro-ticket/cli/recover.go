package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

type recoveryReport struct {
	Findings []string          `json:"findings"`
	Held     map[string]string `json:"held,omitempty"`
}

func runRecover(ctx context.Context, c *call) error {
	if err := c.parse(); err != nil {
		return err
	}
	if err := c.noArgs(); err != nil {
		return err
	}
	loc, err := c.location()
	if err != nil {
		return err
	}
	if loc.boardID == "" {
		return usagef("recover: select a T3 runtime board")
	}
	dir := filepath.Join(loc.localDir, "runs")
	var report recoveryReport
	ran := false
	err = c.withLook(ctx, func(ctx context.Context) error {
		ran = true
		return c.withStore(ctx, func(s ticket.Store) error {
			var err error
			report, err = c.reconcile(ctx, s, dir, loc.boardID)
			return err
		})
	})
	if err != nil {
		return err
	}
	if !ran {
		report.Findings = []string{"planner already holds the lease; requested another pass"}
	}
	return c.out(report, func(w io.Writer) {
		for _, line := range report.Findings {
			fmt.Fprintln(w, line)
		}
		if len(report.Findings) == 0 {
			fmt.Fprintln(w, "no discrepancies")
		}
	})
}

// Reconcile both sides of worker ownership before routing a dead run.
func (c *call) reconcile(ctx context.Context, s ticket.Store, dir, board string) (recoveryReport, error) {
	out := recoveryReport{Findings: []string{}, Held: map[string]string{}}
	procs, err := runs.NativeProcesses(board)
	if err != nil {
		return out, err
	}
	records, err := runs.List(dir, time.Now(), true)
	if err != nil {
		return out, err
	}
	byID := map[string]runs.Record{}
	byLaunch := map[int64]bool{}
	for _, r := range records {
		if r.State == runs.Unreadable {
			return out, errors.New("recover: unreadable run record " + r.ID)
		}
		byID[r.ID] = r
		byLaunch[r.LaunchID] = true
	}
	for _, p := range procs {
		r, ok := byID[p.ID]
		if !ok || r.BoardID != board || r.PID != p.PID || r.Stamp != p.Stamp {
			out.Findings = append(out.Findings, fmt.Sprintf("%s PID %d: live worker lacks a matching stamped record; left running", p.ID, p.PID))
			if r.Ticket != "" {
				out.Held[r.Ticket] = "worker identity uncertain"
			}
			continue
		}
		if runs.NeedsTracker(dir, r) {
			if err := c.startTracker(r.ID); err != nil {
				return out, err
			}
			out.Findings = append(out.Findings, fmt.Sprintf("%s %s: supervisor gone; tracking existing PID %d with its reservation", r.ID, r.Ticket, p.PID))
		}
	}
	host, err := c.hostID()
	if err != nil {
		return out, err
	}
	if boardStore, ok := s.(interface {
		OpenLaunches(context.Context) ([]ticket.Launch, error)
	}); ok {
		launches, err := boardStore.OpenLaunches(ctx)
		if err != nil {
			return out, err
		}
		for _, l := range launches {
			if l.Host != host || byLaunch[l.ID] {
				continue
			}
			line := fmt.Sprintf("launch %d: board entry has no matching run record on this host", l.ID)
			out.Findings = append(out.Findings, line)
			if l.Ticket != 0 {
				out.Held[l.Ticket.String()] = line
			}
		}
	}
	for _, r := range records {
		if r.State != runs.Died || r.EndedAt != nil {
			continue
		}
		line := r.ID + " " + r.Ticket + ": supervisor and worker gone"
		if r.Dir != "" {
			cc := *c
			cc.env.Dir = r.Dir
			head, err := cc.git("rev-parse", "HEAD")
			if err == nil {
				if changes, e := cc.git("status", "--porcelain"); e == nil {
					line += fmt.Sprintf("; worktree at %s, pending edits %t", strings.TrimSpace(head), strings.TrimSpace(changes) != "")
				}
			}
		}
		out.Findings = append(out.Findings, line)
	}
	c.ringOrphans(ctx, dir, "")
	if err := c.recoverReviews(ctx, s, dir, board, records, &out); err != nil {
		return out, err
	}
	slices.Sort(out.Findings)
	return out, nil
}

func (c *call) startTracker(id string) error {
	store, _, err := c.storePath()
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "track-worker", id, "--store", store)
	cmd.Env = slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, runs.RunIDEnv+"=") })
	cmd.SysProcAttr = runs.Detach()
	return startSupervisor(cmd)
}

func runTrackWorker(ctx context.Context, c *call) error {
	if err := c.parse(); err != nil {
		return err
	}
	if len(c.args) != 1 || !runs.ValidID(c.args[0]) {
		return usagef("track-worker: want one native run id")
	}
	loc, err := c.location()
	if err != nil {
		return err
	}
	if loc.boardID == "" {
		return usagef("track-worker: select a T3 board")
	}
	dir := filepath.Join(loc.localDir, "runs")
	ok, err := runs.TrackNative(ctx, dir, c.args[0], loc.boardID)
	if ok {
		c.ringOrphans(ctx, dir, "")
	}
	return err
}

var reviewedHead = regexp.MustCompile(`^Review: GO at ([0-9a-f]{40})(?: |$)`)
var passedHead = regexp.MustCompile(`^QA: pass at ([0-9a-f]{40})(?: |$)`)

// Read proof in ticket order. A later kickback, new handoff or retest invalidates it.
func recoveryProof(d ticket.Detail) (review ticket.Comment, head, qa string) {
	for _, note := range d.Comments {
		line := firstLine(note.Body)
		switch {
		case strings.HasPrefix(line, "Kickback:"), strings.HasPrefix(line, "Rereview:"), strings.HasPrefix(line, "Handoff:"):
			review, head, qa = ticket.Comment{}, "", ""
		case strings.HasPrefix(line, "Review:"):
			review, head, qa = ticket.Comment{}, "", ""
			if m := reviewedHead.FindStringSubmatch(line); m != nil && ticket.HolderBot(note.Author) == "reviewer" {
				review, head = note, m[1]
			}
		case strings.HasPrefix(line, "QA:"), strings.HasPrefix(line, "Retest:"):
			qa = ""
			if m := passedHead.FindStringSubmatch(line); m != nil && ticket.HolderBot(note.Author) == "qa-lead" {
				qa = m[1]
			}
		}
	}
	return
}

func qaReceiptID(id string, review ticket.Comment) string {
	return fmt.Sprintf("%s.review-%d.qa", id, review.ID)
}

type recoveryPR struct {
	Number                                      int
	State, HeadRefName, HeadRefOid, BaseRefName string
	IsCrossRepository                           bool
}

func (c *call) recoveryPR(ctx context.Context, branch string) (recoveryPR, error) {
	var prs []recoveryPR
	cmd := exec.CommandContext(ctx, "gh", "pr", "list", "--head", branch, "--state", "all", "--limit", "2", "--json", "number,state,headRefName,headRefOid,baseRefName,isCrossRepository")
	cmd.Dir = c.env.Dir
	b, err := cmd.Output()
	if err != nil || json.Unmarshal(b, &prs) != nil || len(prs) != 1 || prs[0].IsCrossRepository || prs[0].HeadRefName != branch {
		return recoveryPR{}, errors.New("cannot verify one same-repository PR for the board branch")
	}
	common, err := c.git("rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return recoveryPR{}, err
	}
	setup := filepath.Join(filepath.Dir(strings.TrimSpace(common)), ".maestro", "project", "project-setup.md")
	data, err := os.ReadFile(setup)
	if err != nil {
		return recoveryPR{}, errors.New("cannot read the project's main branch")
	}
	if main := setupLine(string(data), "main branch"); main == "" || prs[0].BaseRefName != main {
		return recoveryPR{}, errors.New("PR base differs from the project's main branch")
	}
	return prs[0], nil
}

func (c *call) recoverReviews(ctx context.Context, s ticket.Store, dir, board string, records []runs.Record, report *recoveryReport) error {
	co, err := c.coord()
	if err != nil {
		return err
	}
	reviews, err := s.List(ctx, ticket.ListFilter{Statuses: []ticket.Status{ticket.StatusInReview}})
	if err != nil {
		return err
	}
	live := map[string]bool{}
	for _, r := range records {
		if !r.Ended() {
			live[r.Ticket] = true
		}
	}
	for _, t := range reviews {
		id := t.ID.String()
		if live[id] || t.LiveClaim() != nil || report.Held[id] != "" {
			continue
		}
		d, err := s.Get(ctx, t.ID)
		if err != nil {
			return err
		}
		note, head, qa := recoveryProof(d)
		if head == "" {
			continue
		}
		key := qaReceiptID(id, note)
		if _, err := co.s.read("receipts", key); err == nil && qa == "" {
			report.Held[id] = "QA handoff received"
			continue
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		pr, err := c.recoveryPR(ctx, "maestro/"+board+"/"+id)
		if err != nil {
			report.Held[id] = err.Error()
			report.Findings = append(report.Findings, id+": "+err.Error())
			continue
		}
		if err := c.mayStart(); err != nil {
			report.Held[id] = err.Error()
			continue
		}
		switch {
		case pr.State == "MERGED" && qa == head && pr.HeadRefOid == head:
			_, err = s.Transition(ctx, ticket.TransitionRequest{ID: t.ID, From: ticket.StatusInReview, To: ticket.StatusDone, Actor: "planner", ActorRole: "planner", ExpectVersion: d.Version, IdempotencyKey: key + "-merged", Note: fmt.Sprintf("Recovered QA closure for PR %d at %s", pr.Number, head)})
			if err != nil {
				return err
			}
			report.Findings = append(report.Findings, id+": verified review, QA and merged PR at "+head+"; closed")
		case pr.State != "OPEN":
			report.Held[id] = "PR closed without matching merge evidence"
			report.Findings = append(report.Findings, id+": "+report.Held[id])
		case pr.HeadRefOid != head:
			_, err = s.Comment(ctx, ticket.CommentRequest{ID: t.ID, Actor: "planner", Body: "Rereview: PR head changed after review at " + head, IdempotencyKey: key + "-rereview-" + pr.HeadRefOid})
			if err != nil {
				return err
			}
			report.Findings = append(report.Findings, id+": changed PR head; requested fresh review")
		default:
			report.Held[id] = "QA handoff pending"
			notify := filepath.Join(filepath.Dir(dir), "notify")
			if c.handOn(ctx, dir, notify, runs.Record{}, send{"qa-lead", "qa " + id}) {
				report.Held[id] = "QA handoff received"
			}
			report.Findings = append(report.Findings, id+": review passed "+head+"; "+report.Held[id])
		}
	}
	return nil
}
