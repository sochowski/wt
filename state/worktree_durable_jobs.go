package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// This is WT's own v1 contract, not pi-subagents' opaque native admission.
// IDs are WT task/result/review IDs. Conversation numbers are store-local.
func migrateDurableJobs(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE IF NOT EXISTS durable_jobs(
 id TEXT PRIMARY KEY, root_id TEXT NOT NULL REFERENCES roots(id) ON DELETE CASCADE,
 parent_id TEXT NOT NULL, child_id TEXT NOT NULL UNIQUE, reviewer_id TEXT NOT NULL UNIQUE,
 admission TEXT NOT NULL, digest TEXT NOT NULL, state TEXT NOT NULL DEFAULT 'writer',
 result_id TEXT NOT NULL UNIQUE, review_id TEXT NOT NULL UNIQUE,
 result TEXT NOT NULL DEFAULT '', review TEXT NOT NULL DEFAULT '',
 writer_key TEXT NOT NULL DEFAULT '', reviewer_key TEXT NOT NULL DEFAULT '',
 FOREIGN KEY(root_id,parent_id) REFERENCES agent_sessions(root_id,id),
 FOREIGN KEY(root_id,child_id) REFERENCES agent_sessions(root_id,id),
 FOREIGN KEY(root_id,reviewer_id) REFERENCES agent_sessions(root_id,id));
 CREATE TABLE IF NOT EXISTS durable_controls(agent_id TEXT PRIMARY KEY REFERENCES agent_sessions(id) ON DELETE CASCADE, runtime TEXT NOT NULL, key TEXT NOT NULL);
 CREATE INDEX IF NOT EXISTS durable_jobs_parent ON durable_jobs(root_id,parent_id);
 PRAGMA user_version=6;`)
	return err
}

type DurableJobRequest struct {
	Version   int      `json:"version"`
	Operation string   `json:"operation"`
	Task      string   `json:"task"`
	Cwd       string   `json:"cwd"`
	Criteria  []string `json:"criteria"`
}
type DurableJobContract struct {
	DurableJobRequest
	Root               string   `json:"root"`
	Parent             string   `json:"parent"`
	ParentUUID         string   `json:"parent_uuid"`
	ParentConversation int      `json:"parent_conversation"`
	RoleVersion        string   `json:"role_version"`
	WriterTools        []string `json:"writer_tools"`
	ReviewerTools      []string `json:"reviewer_tools"`
}
type DurableJob struct {
	ID       string             `json:"id"`
	Root     string             `json:"root"`
	Parent   string             `json:"parent"`
	Child    string             `json:"child"`
	Reviewer string             `json:"reviewer"`
	Contract DurableJobContract `json:"contract"`
	Digest   string             `json:"digest"`
	State    string             `json:"state"`
	ResultID string             `json:"result_id"`
	ReviewID string             `json:"review_id"`
	Result   json.RawMessage    `json:"result"`
	Review   json.RawMessage    `json:"review"`
	// Transient projection contention is not a job failure or a launch receipt.
	ReconciliationPending bool   `json:"reconciliation_pending,omitempty"`
	Phase                 string `json:"phase,omitempty"`
	BlockedReason         string `json:"blocked_reason,omitempty"`
}
type DurableJobObservation struct {
	ID      string `json:"id"`
	Success bool   `json:"success"`
	// The trusted runtime validates model structure against host-observed tools,
	// command receipts and baseline/after hashes; no model completion tool exists.
	Evidence json.RawMessage `json:"evidence"`
}

func (s *Store) durableJob(id string) (DurableJob, error) {
	var j DurableJob
	var admission, result, review string
	err := s.db.QueryRow(`SELECT id,root_id,parent_id,child_id,reviewer_id,admission,digest,state,result_id,review_id,result,review FROM durable_jobs WHERE id=?`, id).Scan(&j.ID, &j.Root, &j.Parent, &j.Child, &j.Reviewer, &admission, &j.Digest, &j.State, &j.ResultID, &j.ReviewID, &result, &review)
	if err == nil {
		err = json.Unmarshal([]byte(admission), &j.Contract)
	}
	if result != "" {
		j.Result = json.RawMessage(result)
	}
	if review != "" {
		j.Review = json.RawMessage(review)
	}
	return j, err
}
func containsTool(tools []string, name string) bool {
	for _, t := range tools {
		if t == name {
			return true
		}
	}
	return false
}
func durableCeiling(d *DurableSnapshot) []string {
	if len(d.Tools) > 0 {
		return d.Tools
	}
	if d.ReadOnly {
		return []string{"read", "wt_workspace"}
	}
	return []string{"read", "bash", "edit", "write", "wt_workspace"}
}
func (s *Store) reserveDurableJob(root, parent, runtime string, r DurableJobRequest) (DurableJob, error) {
	var empty DurableJob
	if r.Version != 1 || !delegationText(r.Operation, 256) || !delegationText(r.Task, delegationPromptLimit) || len(r.Criteria) == 0 || len(r.Criteria) > 16 {
		return empty, errors.New("invalid bounded durable v1 task/criteria")
	}
	for _, c := range r.Criteria {
		if !delegationText(c, 2048) {
			return empty, errors.New("invalid acceptance criterion")
		}
	}
	cwd, err := canonicalDir(r.Cwd)
	if err != nil || cwd != r.Cwd {
		return empty, errors.New("durable task requires canonical assigned cwd")
	}
	top, err := exec.Command("git", "-C", cwd, "rev-parse", "--show-toplevel").Output()
	if err != nil || strings.TrimSpace(string(top)) != cwd {
		return empty, errors.New("durable writer requires exact canonical git checkout root")
	}
	w, err := s.Worktree(root)
	if err != nil {
		return empty, err
	}
	a, err := w.agent(parent)
	if err != nil {
		return empty, err
	}
	if err = validateDurable(a); err != nil {
		return empty, err
	}
	if a.Adapter.Durable.Job != "" {
		return empty, errors.New("nested durable fanout is forbidden")
	}
	tools := durableCeiling(a.Adapter.Durable)
	writer := []string{}
	for _, t := range []string{"read", "bash", "edit", "write", "wt_workspace"} {
		if containsTool(tools, t) {
			writer = append(writer, t)
		}
	}
	// A writer must be able to validate its work. Never silently upgrade a reader.
	for _, t := range []string{"read", "bash", "edit", "write"} {
		if !containsTool(writer, t) {
			return empty, errors.New("parent ceiling cannot admit built-in writer")
		}
	}
	reviewer := []string{"read"}
	if containsTool(tools, "wt_workspace") {
		reviewer = append(reviewer, "wt_workspace")
	}
	contract := DurableJobContract{DurableJobRequest: r, Root: root, Parent: parent, ParentUUID: a.Adapter.Durable.UUID, ParentConversation: a.Adapter.Durable.Conversation, RoleVersion: "wt-builtins-v1", WriterTools: writer, ReviewerTools: reviewer}
	id := delegationDigest([]byte(jsonText([]string{"wt-durable-job-v1", root, parent, contract.ParentUUID, r.Operation})))
	if prior, e := s.durableJob(id); e == nil {
		if jsonText(prior.Contract) != jsonText(contract) {
			return empty, errors.New("durable task immutable admission mismatch")
		}
		tx, e := s.db.Begin()
		if e != nil {
			return empty, e
		}
		defer tx.Rollback()
		if e = liveActorTx(tx, root, parent, runtime); e != nil {
			return empty, e
		}
		return prior, nil
	} else if e != sql.ErrNoRows {
		return empty, e
	}
	// Existing immutable operations above remain inspectable even if their
	// checkout later outgrows limits. Only NEW admission gets this preflight.
	if a.Runtime != runtime || runtime == "" || a.Stopped {
		return empty, errors.New("stale durable admission owner")
	}
	attached := cwd == w.Cwd
	for _, checkout := range w.Checkouts {
		if checkout.Path == cwd {
			attached = true
		}
	}
	if !attached {
		return empty, errors.New("task cwd must be explicitly attached or session home")
	}
	if err = preflightDurableEvidence(cwd); err != nil {
		return empty, err
	}
	lease, err := lockFile(durableWriterLock(cwd))
	if err != nil {
		return empty, errors.New("assigned checkout writer lease is occupied; use an isolated checkout")
	}
	defer lease.Close()
	// Filesystem preparation is nonexecuting; all agent/budget reservations below
	// are atomic. An interrupted intent retains its UUID and exact store path.
	child, review := newID(), newID()
	snapshots := map[string]*DurableSnapshot{}
	for _, entry := range []struct {
		id, role string
		readOnly bool
		tools    []string
	}{{child, "writer", false, writer}, {review, "reviewer", true, reviewer}} {
		dir := filepath.Join(stateDir(), "durable", root, entry.id)
		if err = os.MkdirAll(dir, 0700); err != nil {
			return empty, err
		}
		dir, err = canonicalDir(dir)
		if err != nil {
			return empty, err
		}
		snapshots[entry.id] = &DurableSnapshot{Store: filepath.Join(dir, "session.sqlite"), UUID: newID(), Conversation: 1, Definition: durableDefinition, Dependencies: durableDependencies, Cwd: cwd, WriterLock: durableWriterLock(cwd), ReadOnly: entry.readOnly, Job: id, Role: entry.role, Tools: entry.tools}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	if err = liveActorTx(tx, root, parent, runtime); err != nil {
		return empty, err
	}
	var allowed, count, active int
	if err = tx.QueryRow(`SELECT count(*) FROM roots r JOIN sessions s ON s.name=r.name WHERE r.id=? AND (s.workspace_path=? OR EXISTS(SELECT 1 FROM checkouts WHERE root_id=r.id AND path=?))`, root, cwd, cwd).Scan(&allowed); err != nil {
		return empty, err
	}
	if allowed != 1 {
		return empty, errors.New("task cwd must be explicitly attached or session home")
	}
	if err = tx.QueryRow(`SELECT count(*) FROM agent_sessions WHERE root_id=?`, root).Scan(&count); err != nil {
		return empty, err
	}
	if count+2 > 8 {
		return empty, errors.New("root cumulative limit: 8 retained agents including mandatory reviewers")
	}
	if err = tx.QueryRow(`SELECT count(*) FROM durable_jobs WHERE root_id=? AND state IN ('writer','review')`, root).Scan(&active); err != nil {
		return empty, err
	}
	if active >= 4 {
		return empty, errors.New("durable parallel limit: 4")
	}
	budget, err := tx.Exec(`UPDATE roots SET wake_budget=wake_budget-2 WHERE id=? AND wake_enabled=1 AND wake_budget>=2`, root)
	if err != nil {
		return empty, err
	}
	n, _ := budget.RowsAffected()
	if n != 1 {
		return empty, errors.New("automatic wake disabled or insufficient writer/reviewer budget")
	}
	j := DurableJob{ID: id, Root: root, Parent: parent, Child: child, Reviewer: review, Contract: contract, Digest: delegationDigest([]byte(jsonText(contract))), State: "writer", ResultID: delegationDigest([]byte(id + ":result:v1")), ReviewID: delegationDigest([]byte(id + ":review:v1"))}
	for _, entry := range []struct{ id, role string }{{child, "writer"}, {review, "reviewer"}} {
		adapter := PiSnapshot{Version: 1, Backend: "durable", Durable: snapshots[entry.id]}
		if _, err = tx.Exec(`INSERT INTO agent_sessions(id,name,root_id,parent,creator,cwd,stopped,adapter) VALUES(?,?,?,?,?,?,1,?)`, entry.id, entry.role+"-"+id[:12], root, parent, parent, cwd, jsonText(adapter)); err != nil {
			return empty, err
		}
	}
	if _, err = tx.Exec(`INSERT INTO views(id,root_id,kind,target,manager) VALUES(?,?,'agent',?,?)`, newID(), root, child, parent); err != nil {
		return empty, err
	}
	if _, err = tx.Exec(`INSERT INTO durable_jobs(id,root_id,parent_id,child_id,reviewer_id,admission,digest,state,result_id,review_id) VALUES(?,?,?,?,?,?,?,?,?,?)`, id, root, parent, child, review, jsonText(contract), j.Digest, j.State, j.ResultID, j.ReviewID); err != nil {
		return empty, err
	}
	return j, tx.Commit()
}
func (s *Store) durableJobHost(root, child, runtime, capability string) (DurableJob, error) {
	var id string
	err := s.db.QueryRow(`SELECT id FROM durable_jobs WHERE root_id=? AND (child_id=? OR reviewer_id=?)`, root, child, child).Scan(&id)
	if err != nil {
		return DurableJob{}, err
	}
	j, err := s.durableJob(id)
	if err != nil {
		return j, err
	}
	column := "writer_key"
	if child == j.Reviewer {
		column = "reviewer_key"
	}
	var key string
	if err = s.db.QueryRow(`SELECT `+column+` FROM durable_jobs WHERE id=?`, id).Scan(&key); err != nil {
		return j, err
	}
	if capability == "" || key == "" || key != delegationDigest([]byte(capability)) {
		return j, errors.New("host observation capability required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return j, err
	}
	defer tx.Rollback()
	if err = liveActorTx(tx, root, child, runtime); err != nil {
		return j, err
	}
	if err = tx.Rollback(); err != nil {
		return j, err
	}
	w, err := s.Worktree(root)
	if err != nil {
		return j, err
	}
	a, err := w.agent(child)
	if err != nil {
		return j, err
	}
	if err = validateDurable(a); err != nil {
		return j, err
	}
	d := a.Adapter.Durable
	role, tools := "writer", j.Contract.WriterTools
	if child == j.Reviewer {
		role, tools = "reviewer", j.Contract.ReviewerTools
	}
	if d.Job != j.ID || d.Role != role || d.ReadOnly != (role == "reviewer") || jsonText(d.Tools) != jsonText(tools) || d.Cwd != j.Contract.Cwd {
		return j, errors.New("durable role/store ceiling mismatch")
	}
	if (role == "writer" && j.State != "writer") || (role == "reviewer" && j.State != "review") {
		return j, errors.New("durable host not in admitted phase")
	}
	return j, nil
}
func (s *Store) finishDurableJob(root, child, runtime, capability string, o DurableJobObservation) error {
	if !delegationHash(o.ID) || len(o.Evidence) == 0 || len(o.Evidence) > delegationResultLimit || !json.Valid(o.Evidence) {
		return errors.New("invalid bounded host observation")
	}
	// A terminal retry must match exact evidence; it cannot grant another launch.
	var id string
	if err := s.db.QueryRow(`SELECT id FROM durable_jobs WHERE root_id=? AND (child_id=? OR reviewer_id=?)`, root, child, child).Scan(&id); err != nil {
		return err
	}
	prior, err := s.durableJob(id)
	if err != nil {
		return err
	}
	expected, raw := prior.ResultID, prior.Result
	if child == prior.Reviewer {
		expected, raw = prior.ReviewID, prior.Review
	}
	if o.ID != expected {
		return errors.New("immutable result/review ID mismatch")
	}
	encoded := jsonText(o)
	if len(raw) > 0 {
		if string(raw) != encoded {
			return errors.New("result/review evidence cannot be replaced")
		}
		return nil
	}
	j, err := s.durableJobHost(root, child, runtime, capability)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = liveActorTx(tx, root, child, runtime); err != nil {
		return err
	}
	next := "failed"
	column := "result"
	if child == j.Child && o.Success {
		next = "review"
	}
	if child == j.Reviewer {
		column = "review"
		if o.Success {
			next = "succeeded"
		}
	}
	res, err := tx.Exec(`UPDATE durable_jobs SET `+column+`=?,state=? WHERE id=? AND state=? AND `+column+`=''`, encoded, next, id, j.State)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return errors.New("durable publication raced")
	}
	if _, err = tx.Exec(`UPDATE agent_sessions SET stopped=1,status='idle' WHERE root_id=? AND id=? AND runtime=?`, root, child, runtime); err != nil {
		return err
	}
	if next == "review" {
		if _, err = tx.Exec(`INSERT INTO views(id,root_id,kind,target,manager) VALUES(?,?,'agent',?,?)`, newID(), root, j.Reviewer, j.Parent); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Observe a failed owned host without fabricating a worker result/review.
// Exact runtime and admitted phase guard against stale exits racing publication.
func (s *Store) pauseFailedDurableJobHost(a AgentSession) error {
	d := a.Adapter.Durable
	if d == nil || d.Job == "" {
		return nil
	}
	phase, childColumn := "writer", "child_id"
	if d.Role == "reviewer" {
		phase, childColumn = "review", "reviewer_id"
	} else if d.Role != "writer" {
		return errors.New("invalid durable failure role")
	}
	_, err := s.db.Exec(`UPDATE agent_sessions SET stopped=1,status='error' WHERE root_id=? AND id=? AND runtime=? AND EXISTS(SELECT 1 FROM durable_jobs j WHERE j.id=? AND j.root_id=? AND j.`+childColumn+`=agent_sessions.id AND j.state=?)`, a.RootID, a.ID, a.Runtime, d.Job, a.RootID, phase)
	return err
}

// Startup/relaunch uses the retained store. Only unfinished phases can run.
func (s *Store) durableJobRunnable(a AgentSession) error {
	d := a.Adapter.Durable
	if d == nil || d.Job == "" {
		return nil
	}
	j, err := s.durableJob(d.Job)
	if err != nil {
		return err
	}
	if (a.ID == j.Child && j.State == "writer") || (a.ID == j.Reviewer && j.State == "review") {
		return nil
	}
	return errors.New("durable task is terminal or awaiting mandatory review; no relaunch")
}
func (s *Store) reconcileDurableJob(root, parent, runtime, id string) (DurableJob, error) {
	j, err := s.durableJob(id)
	if err != nil {
		return j, err
	}
	if j.Root != root || j.Parent != parent {
		return j, errors.New("wrong durable task owner")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return j, err
	}
	if err = liveActorTx(tx, root, parent, runtime); err != nil {
		tx.Rollback()
		return j, err
	}
	tx.Rollback()
	return s.advanceDurableJob(id)
}

// An already-admitted mandatory reviewer is independent of parent liveness.
// This never reserves another child or wake permit.
func (s *Store) advanceDurableJob(id string) (DurableJob, error) {
	j, err := s.durableJob(id)
	if err != nil {
		return j, err
	}
	root := j.Root
	w, err := s.Worktree(root)
	if err != nil {
		return j, err
	}
	// Failure/explicit stop is durable status, not a projection operation. Report
	// it even while another root reflow holds the topology lock.
	if j.State == "writer" || j.State == "review" {
		child := j.Child
		if j.State == "review" {
			child = j.Reviewer
		}
		a, e := w.agent(child)
		if e != nil {
			return j, e
		}
		if a.Stopped && (a.Status == "error" || a.Adapter.Durable.Initialized) {
			j.Phase = j.State
			j.State = "blocked"
			j.BlockedReason = "durable-host-stopped; explicit human recovery required, no automatic relaunch"
			if a.Status == "error" {
				j.BlockedReason = "durable-host-failed; inspect retained store and use explicit human recovery, not automatic relaunch"
			}
			return j, nil
		}
	}
	lock, err := s.rootLock(w)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		// A concurrent reconciler owns projection. Return current durable evidence
		// without touching panes, bootstrapping another owner, or spending permits.
		j, err = s.durableJob(id)
		j.ReconciliationPending = err == nil
		return j, err
	}
	if err != nil {
		return j, err
	}
	// Reload after locking: a host may have published while we waited.
	j, err = s.durableJob(id)
	if err != nil {
		lock.Close()
		return j, err
	}
	w, err = s.Worktree(root)
	if err != nil {
		lock.Close()
		return j, err
	}
	defer lock.Close()
	if j.State == "writer" || j.State == "review" {
		child := j.Child
		if j.State == "review" {
			child = j.Reviewer
		}
		a, e := w.agent(child)
		if e != nil {
			return j, e
		}
		if a.Stopped && (a.Status == "error" || a.Adapter.Durable.Initialized) {
			j.Phase = j.State
			j.State = "blocked"
			j.BlockedReason = "durable-host-stopped; explicit human recovery required, no automatic relaunch"
			if a.Status == "error" {
				j.BlockedReason = "durable-host-failed; inspect retained store and use explicit human recovery, not automatic relaunch"
			}
			return j, nil
		}
		if !a.Adapter.Durable.Initialized {
			if e = s.initializeDurable(w, child); e != nil {
				if pauseErr := s.pauseFailedDurableJobHost(a); pauseErr != nil {
					return j, pauseErr
				}
				return j, e
			}
			w, e = s.Worktree(root)
			if e != nil {
				return j, e
			}
		}
		// Concurrent reservations can have projected a waiting-bootstrap shell.
		// Only this exact host-owned placeholder can be replaced automatically;
		// never overwrite a pin or a shell a human has started using.
		a, e = w.agent(child)
		if e != nil {
			return j, e
		}
		for _, v := range w.Views {
			if v.Kind == "agent" && v.Target == child && v.Problem == "durable task awaiting exact bootstrap" && !a.Stopped {
				p := livePane(w, v.ID)
				if p != "" {
					if v.Pinned || v.Manager != j.Parent || humanUsingPane(p) {
						return j, errors.New("durable task placeholder is human/pin protected")
					}
					if e = s.launchView(w, v, p); e != nil {
						return j, e
					}
				}
			}
		}
		// A healthy exact child needs no topology restore on every status poll.
		// Missing/dead panes still use the existing fenced restoration path.
		if a.Runtime != "" && !a.Stopped {
			live, e := s.liveRoot(w)
			if e != nil {
				return j, e
			}
			if live {
				for _, v := range w.Views {
					if v.Kind == "agent" && v.Target == child && v.Problem == "" {
						if p := livePane(w, v.ID); p != "" {
							dead, e := tmux("display-message", "-p", "-t", p, "#{pane_dead}")
							if e != nil {
								return j, e
							}
							if dead == "0" {
								return s.durableJob(id)
							}
						}
					}
				}
			}
		}
		// Do not resume an explicitly stopped initialized child. Crash/recoverable
		// close has stopped=false and runs with the same task/submission identity.
		if e = s.restore(w); e != nil {
			return j, e
		}
	}
	return s.durableJob(id)
}
func durableJobsCommand(s *Store, op string, input io.Reader) (any, error) {
	data, err := io.ReadAll(io.LimitReader(input, 1024*1024+1))
	if err != nil || len(data) > 1024*1024 {
		return nil, errors.New("bounded durable job command required")
	}
	var r struct {
		Root        string                `json:"root"`
		Parent      string                `json:"parent"`
		Child       string                `json:"child"`
		Runtime     string                `json:"runtime"`
		Capability  string                `json:"capability"`
		Job         string                `json:"job"`
		Request     DurableJobRequest     `json:"request"`
		Observation DurableJobObservation `json:"observation"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&r); err != nil {
		return nil, err
	}
	if dec.Decode(new(any)) != io.EOF {
		return nil, errors.New("one durable job command required")
	}
	switch op {
	case "reserve":
		return s.reserveDurableJob(r.Root, r.Parent, r.Runtime, r.Request)
	case "reconcile":
		return s.reconcileDurableJob(r.Root, r.Parent, r.Runtime, r.Job)
	case "host":
		return s.durableJobHost(r.Root, r.Child, r.Runtime, r.Capability)
	case "finish":
		err = s.finishDurableJob(r.Root, r.Child, r.Runtime, r.Capability, r.Observation)
		pending := false
		if err == nil {
			var id string
			if e := s.db.QueryRow(`SELECT id FROM durable_jobs WHERE root_id=? AND child_id=? AND state='review'`, r.Root, r.Child).Scan(&id); e == nil {
				advanced, advanceErr := s.advanceDurableJob(id)
				pending = advanceErr != nil || advanced.ReconciliationPending // Publication remains durable; parent retries placement only.
			}
		}
		return struct {
			OK                  bool `json:"ok"`
			ReviewLaunchPending bool `json:"review_launch_pending"`
		}{err == nil, pending}, err
	case "list":
		w, e := s.Worktree(r.Root)
		if e != nil {
			return nil, e
		}
		if e = validateActor(w, r.Parent); e != nil {
			return nil, e
		}
		a, e := w.agent(r.Parent)
		if e != nil || a.Runtime != r.Runtime || a.Stopped {
			return nil, errors.New("stale durable owner")
		}
		rows, e := s.db.Query(`SELECT id FROM durable_jobs WHERE root_id=? AND parent_id=? ORDER BY id`, r.Root, r.Parent)
		if e != nil {
			return nil, e
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return nil, e
			}
			ids = append(ids, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, e
		}
		jobs := []DurableJob{}
		for _, id := range ids {
			j, e := s.durableJob(id)
			if e != nil {
				return nil, e
			}
			jobs = append(jobs, j)
		}
		return jobs, nil
	}
	return nil, errors.New("unknown WT durable v1 operation")
}
