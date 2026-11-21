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
	"strconv"
	"strings"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/mongostore"
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
	for _, file := range []string{"reconsider.active", "reconsider.at"} {
		if at, e := readHold(filepath.Join(loc.localDir, file)); e != nil {
			return e
		} else if !at.IsZero() {
			if e := c.scheduleReconsider(loc.localDir, at); e != nil {
				return e
			}
		}
	}
	var report recoveryReport
	ran := false
	err = c.withLook(ctx, func(ctx context.Context) error {
		ran = true
		return c.withStore(ctx, func(s ticket.Store) error {
			var err error
			report, err = c.reconcile(ctx, s, dir, loc.boardID)
			if err != nil {
				return err
			}
			if err := recordRecoveredBoot(loc.localDir); err != nil {
				return err
			}
			return c.dispatchRuntimeLook(ctx, s, dir, &report, false)
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
	byLaunch := map[int64]runs.Record{}
	byTicket := map[string]bool{}
	for _, r := range records {
		if r.State == runs.Unreadable {
			return out, errors.New("recover: unreadable run record " + r.ID)
		}
		byID[r.ID] = r
		byLaunch[r.LaunchID] = r
		byTicket[r.Ticket] = true
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
			if l.Host != host {
				continue
			}
			if r, ok := byLaunch[l.ID]; ok {
				if r.EndedAt != nil && r.BoardID == board && r.PID == l.PID && r.Stamp == l.Stamp {
					outcome := ticket.LaunchResult
					if r.State == runs.Died {
						outcome = ticket.LaunchDied
					} else if r.IsError || r.State != runs.Done {
						outcome = ticket.LaunchNoResult
					}
					req := ticket.EndLaunchRequest{ID: l.ID, Outcome: outcome, Session: r.SessionID}
					if outcome, reset := routeOutcome(r); outcome == outUsageLimit {
						req.Lifts = limitLifts(r, reset)
					}
					if _, err := s.EndLaunch(ctx, req); err != nil {
						return out, err
					}
					out.Findings = append(out.Findings, r.ID+": recovered end of board launch")
				}
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
			checkout, found, e := c.buildCheckout(r)
			if e != nil {
				out.Held[r.Ticket] = "verify saved build worktree"
				out.Findings = append(out.Findings, line+"; cannot locate saved build worktree")
				continue
			}
			cc.env.Dir = checkout
			if r.Job == "build" && !found {
				line += "; no registered ticket checkout, inspecting launch directory"
			}
			head, err := cc.git("rev-parse", "HEAD")
			if err == nil {
				if changes, e := cc.git("status", "--porcelain"); e == nil {
					line += fmt.Sprintf("; worktree at %s, pending edits %t", strings.TrimSpace(head), strings.TrimSpace(changes) != "")
				}
			}
		}
		out.Findings = append(out.Findings, line)
	}
	active, err := s.List(ctx, ticket.ListFilter{Statuses: []ticket.Status{ticket.StatusInProgress}})
	if err != nil {
		return out, err
	}
	for _, t := range active {
		if !byTicket[t.ID.String()] && out.Held[t.ID.String()] == "" {
			out.Held[t.ID.String()] = "in-progress ticket has no local run; verify its branch and claim"
			out.Findings = append(out.Findings, t.ID.String()+": "+out.Held[t.ID.String()])
		}
	}
	co, err := c.coord()
	if err != nil {
		return out, err
	}
	prior, err := co.s.list("recovery-holds")
	if err != nil {
		return out, err
	}
	for id, reason := range out.Held {
		if err := co.s.write("recovery-holds", id, reason); err != nil {
			return out, err
		}
	}
	for _, old := range prior {
		if out.Held[old.name] == "" {
			if err := co.s.remove("recovery-holds", old.name); err != nil && !errors.Is(err, os.ErrNotExist) {
				return out, err
			}
		}
	}
	c.ringOrphans(ctx, dir, "")
	notify := filepath.Join(filepath.Dir(dir), "notify")
	for _, r := range records {
		if r.EndedAt != nil && r.BoardID == board && codeRole(notify, r.Owner()) {
			if err := c.replayCode(ctx, dir, notify, r); err != nil {
				return out, err
			}
		}
	}
	if err := c.recoverReviews(ctx, s, dir, board, records, &out); err != nil {
		return out, err
	}
	if b, err := c.board(); err == nil && codeRole(filepath.Join(filepath.Dir(dir), "notify"), "planner") {
		pending, err := b.PendingDeliveries(ctx, "")
		if err != nil {
			return out, err
		}
		failing := map[string]bool{}
		for _, d := range pending {
			if _, err = c.deliver(ctx, b, d.ID, ""); err != nil {
				out.Findings = append(out.Findings, d.Goal+": "+err.Error())
				switch d.Job {
				case "stop", "start", "rules":
					// Each failed replay of one control doubles its wait, from a minute up to an hour.
					name := d.Hash()
					failing[name] = true
					tries := 0
					m, err := co.s.read("control-retries", name)
					if err == nil {
						tries, _ = strconv.Atoi(m.body)
					} else if !errors.Is(err, os.ErrNotExist) {
						return out, err
					}
					if err := co.s.write("control-retries", name, strconv.Itoa(tries+1)); err != nil {
						return out, err
					}
					wait := min(time.Minute<<min(tries, 6), time.Hour)
					if err := c.scheduleReconsider(filepath.Dir(dir), time.Now().Add(wait)); err != nil {
						return out, err
					}
				}
			}
		}
		retries, err := co.s.list("control-retries")
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return out, err
		}
		for _, m := range retries {
			if !failing[m.name] {
				if err := co.s.remove("control-retries", m.name); err != nil && !errors.Is(err, os.ErrNotExist) {
					return out, err
				}
			}
		}
	}
	if b, err := c.board(); err == nil {
		pending, err := b.HasPendingGoalItem(ctx)
		if err != nil {
			return out, err
		}
		if pending {
			h, err := runs.Webhook(filepath.Join(filepath.Dir(dir), "notify"), "chief-of-staff")
			if err == nil && h.Exec != "" {
				if err := c.wakeGoalInbox(ctx, b, dir, h); err != nil {
					out.Findings = append(out.Findings, "chief wake pending")
				}
			}
		}
	}
	slices.Sort(out.Findings)
	return out, nil
}

// A build can create its ticket checkout after the supervisor records the launch directory.
func (c *call) buildCheckout(r runs.Record) (string, bool, error) {
	if r.BoardID == "" || r.Job != "build" || r.Ticket == "" {
		return r.Dir, false, nil
	}
	cc := *c
	cc.env.Dir = r.Dir
	rows, err := cc.git("worktree", "list", "--porcelain", "-z")
	if err != nil {
		return "", false, err
	}
	branch := "branch refs/heads/maestro/" + r.BoardID + "/" + r.Ticket
	for _, row := range strings.Split(rows, "\x00\x00") {
		fields := strings.Split(row, "\x00")
		if slices.Contains(fields, branch) {
			dir, ok := strings.CutPrefix(fields[0], "worktree ")
			info, err := os.Stat(dir)
			if !ok || err != nil || !info.IsDir() {
				return "", false, errors.New("registered build worktree unavailable")
			}
			return dir, true, nil
		}
	}
	return r.Dir, false, nil
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

// A planner answer to a QA escalation starts another QA run at the same review.
func qaReceiptID(d ticket.Detail, review ticket.Comment, head string) string {
	key := fmt.Sprintf("%s.review-%d.qa", d.ID, review.ID)
	escalation, ok := latestWith(d, "Escalation:")
	if !ok || escalation.ID <= review.ID || !strings.HasPrefix(escalation.Body, "Escalation: QA at ") || shaIn(escalation.Body) != head {
		return key
	}
	for i := len(d.Comments) - 1; i >= 0 && d.Comments[i].ID > escalation.ID; i-- {
		answer := d.Comments[i]
		if strings.HasPrefix(answer.Body, "Answer:") && ticket.HolderBot(answer.Author) == "planner" {
			return fmt.Sprintf("%s.escalation-%d", key, escalation.ID)
		}
	}
	return key
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
	if err != nil || json.Unmarshal(b, &prs) != nil || len(prs) != 1 || prs[0].IsCrossRepository || prs[0].HeadRefName != branch || len(prs[0].HeadRefOid) != 40 || !shaPattern.MatchString(prs[0].HeadRefOid) {
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
		if head == "" || d.Criteria != nil && d.Criteria.Review != head {
			continue
		}
		key := qaReceiptID(d, note, head)
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
		case pr.State == "MERGED" && qa == head && pr.HeadRefOid == head && (d.Criteria == nil || d.Criteria.QA == head && d.Criteria.Merge == head):
			_, err = s.Transition(ctx, ticket.TransitionRequest{ID: t.ID, From: ticket.StatusInReview, To: ticket.StatusDone, Actor: "planner", ActorRole: "planner", ExpectVersion: d.Version, IdempotencyKey: key + "-merged", Note: fmt.Sprintf("Recovered QA closure for PR %d at %s", pr.Number, head)})
			if err != nil {
				return err
			}
			report.Findings = append(report.Findings, id+": verified review, QA and merged PR at "+head+"; closed")
		case pr.State == "CLOSED" && d.Criteria != nil && d.Criteria.Merge == head:
			b, err := c.board()
			if err != nil {
				return err
			}
			if _, err := b.ChangeCriteria(ctx, mongostore.CriteriaRequest{Verb: "closed-unmerged", ID: t.ID, Head: head, Actor: "planner"}, key+"-closed-unmerged"); err != nil {
				return err
			}
			report.Held[id] = "PR closed without merging; released merge reservation"
			report.Findings = append(report.Findings, id+": "+report.Held[id])
		case pr.State != "OPEN":
			report.Held[id] = "PR closed without matching merge evidence"
			report.Findings = append(report.Findings, id+": "+report.Held[id])
		case pr.HeadRefOid != head:
			if d.Criteria != nil && d.Criteria.Merge == head {
				// New merge requests pin the old SHA synchronously. A different head cannot satisfy one.
				b, err := c.board()
				if err != nil {
					return err
				}
				if _, err := b.ChangeCriteria(ctx, mongostore.CriteriaRequest{Verb: "head-moved", ID: t.ID, Head: head, Actor: "planner"}, key+"-unreserve-"+pr.HeadRefOid); err != nil {
					return err
				}
			}
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

// Ordinary worker wakes share the same lease as startup recovery. Consume
// only the events present before the look; a concurrent arrival stays pending.
func (c *call) dispatchRuntimeLook(ctx context.Context, s ticket.Store, dir string, report *recoveryReport, force bool) error {
	notify := filepath.Join(filepath.Dir(dir), "notify")
	if !codeRole(notify, "planner") {
		return nil
	}
	co, err := c.coord()
	if err != nil {
		return err
	}
	pending, err := co.s.list("planner-pending")
	if err != nil {
		return err
	}
	if !force && len(pending) == 0 {
		return nil
	}
	if err := c.mayStart(); err != nil {
		if errors.Is(err, ticket.ErrStopped) || errors.Is(err, ticket.ErrPaused) {
			return nil
		}
		return err
	}
	cc := *c
	cc.actor, cc.runCache = "planner", nil
	out, err := cc.look(ctx, s, lookOpts{recovered: report, limit: -1, stall: ticket.DefaultStall, handouts: co, runs: dir, host: co.host})
	if err != nil {
		return err
	}
	cc.startSends(ctx, dir, &out)
	if len(out.Judge) > 0 {
		rules, _ := cc.standingRules()
		cc.startJudge(ctx, &out, rules, co)
	}
	for _, sd := range out.Send {
		if id := ticketInLine.FindString(sd.Line); id != "" {
			co.dropHandout(id, sd.Bot)
		}
	}
	if len(out.Errors) > 0 {
		return errors.New(strings.Join(out.Errors, "; "))
	}
	if len(out.Send) > 0 {
		return nil
	}
	for _, m := range pending {
		if err := co.s.remove("planner-pending", m.name); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

type lookOwnerKey struct{}

func bootStamp() ([]byte, error) { return os.ReadFile("/proc/sys/kernel/random/boot_id") }
func recordRecoveredBoot(root string) error {
	stamp, err := bootStamp()
	if err != nil {
		return err
	}
	return writeWholeMode(filepath.Join(root, "recovered.boot"), stamp, 0600)
}

// Manual starts also run startup recovery if the system service has not run
// in this Linux boot. Starts inside the lease share its current reconciliation.
func (c *call) ensureRuntimeReady(ctx context.Context) error {
	loc, err := c.location()
	if err != nil || loc.boardID == "" {
		return err
	}
	if ctx.Value(lookOwnerKey{}) == loc.boardID {
		return nil
	}
	current, err := bootStamp()
	if err != nil {
		return err
	}
	path := filepath.Join(loc.localDir, "recovered.boot")
	saved, err := os.ReadFile(path)
	if err == nil && string(saved) == string(current) {
		return nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	err = c.withLook(ctx, func(ctx context.Context) error {
		b, err := c.board()
		if err != nil {
			return err
		}
		cc := *c
		cc.actor = "planner"
		if _, err = cc.reconcile(ctx, b, filepath.Join(loc.localDir, "runs"), loc.boardID); err != nil {
			return err
		}
		return recordRecoveredBoot(loc.localDir)
	})
	if err != nil {
		return err
	}
	saved, err = os.ReadFile(path)
	if err != nil || string(saved) != string(current) {
		return fmt.Errorf("%w: startup recovery owns the board", ticket.ErrPaused)
	}
	return nil
}
