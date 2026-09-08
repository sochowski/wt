package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"syscall"
	"time"
)

type delegationHostLaunch struct {
	Owner   DelegationOwner   `json:"owner"`
	Runtime string            `json:"runtime"`
	Job     string            `json:"job"`
	Turn    string            `json:"turn"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	Cwd     string            `json:"cwd"`
}

// Dedicated fresh-host path. Restoring a missing host still hits the ordinary
// launch fence; neither an uncertain reservation nor a dead host is replayed.
func (s *Store) launchDelegationHost(input io.Reader) (any, error) {
	var request delegationHostLaunch
	body, err := io.ReadAll(io.LimitReader(input, 1024*1024+1))
	if err != nil || len(body) > 1024*1024 {
		return nil, errors.New("native host launch exceeds its input bound")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return nil, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("native host requires one bounded launch object")
	}
	job, turn, _, err := s.delegationStatus(request.Owner, request.Runtime, request.Job, request.Turn)
	if err != nil {
		return nil, err
	}
	w, err := s.Worktree(job.RootID)
	if err != nil {
		return nil, err
	}
	lock, err := s.rootLock(w)
	lockDeadline := time.Now().Add(5 * time.Second)
	for errors.Is(err, syscall.EWOULDBLOCK) && time.Now().Before(lockDeadline) {
		time.Sleep(20 * time.Millisecond)
		lock, err = s.rootLock(w)
	}
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	job, turn, _, err = s.delegationStatus(request.Owner, request.Runtime, request.Job, request.Turn)
	if err != nil {
		return nil, err
	}
	// Another placement/checkpoint may have run while this job waited.
	w, err = s.Worktree(job.RootID)
	if err != nil {
		return nil, err
	}
	if live, err := s.liveRoot(w); err != nil || !live {
		return nil, errors.New("native host requires its existing live WT root")
	}
	a, err := w.agent(job.ChildID)
	if err != nil {
		return nil, err
	}
	if turn.State != "queued" || job.NativeID != "" || a.Runtime != "" || a.Stopped || request.Cwd != a.Cwd || !filepath.IsAbs(request.Command) || len(request.Args) == 0 || request.Env["PI_SUBAGENT_REQUIRED_NATIVE_PROVIDER"] != request.Owner.Provider {
		return nil, errors.New("native host launch mismatch, already attempted, or non-fresh conversation; no replay")
	}
	if err = s.validateDelegationHostCommand(job, request); err != nil {
		return nil, err
	}
	var view View
	for _, v := range w.Views {
		if v.Kind == "agent" && v.Target == a.ID {
			view = v
			break
		}
	}
	if view.ID == "" || livePane(w, view.ID) != "" {
		return nil, errors.New("native host view is absent or already placed")
	}
	if err = os.MkdirAll(runtimeDir(), 0700); err != nil {
		return nil, err
	}
	token := newID()
	request.Runtime = token
	payload := filepath.Join(runtimeDir(), job.ID+".native-launch.json")
	file, err := os.OpenFile(payload, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	err = json.NewEncoder(file).Encode(request)
	closeErr := file.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	result, err := s.db.Exec(`UPDATE agent_sessions SET runtime=? WHERE root_id=? AND id=? AND runtime=''`, token, w.ID, a.ID)
	if err != nil {
		return nil, err
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return nil, errors.New("native host launch was already claimed")
	}
	// Detached placement never changes the client's selected pane/window.
	pane, err := tmux("new-window", "-d", "-P", "-F", "#{pane_id}", "-t", "="+w.Name, "-n", a.Name, "bash --noprofile --norc -i")
	if err != nil {
		return nil, err
	}
	if err = s.bindView(w, view, pane); err != nil {
		return nil, err
	}
	command := shellArgs("env", "WT_DB="+dbPath(), "WT_STATUS_DIR="+stateDir(), runtimeBinary(), "worktree", "_run-native-host", w.ID, a.ID, token, payload)
	if _, err = tmux("respawn-pane", "-k", "-t", pane, "-c", a.Cwd, command); err != nil {
		return nil, err
	}
	if err = s.reflow(w); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(payload + ".pid"); err == nil {
			pid, err := strconv.Atoi(string(data))
			if err != nil {
				return nil, err
			}
			return struct {
				PID   int    `json:"pid"`
				Pane  string `json:"pane"`
				Child string `json:"child"`
			}{pid, pane, a.ID}, nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return nil, errors.New("native host startup unobserved; reservation retained, never replay automatically")
}

func (s *Store) runDelegationHost(root, child, token, payload string) error {
	w, err := s.Worktree(root)
	if err != nil {
		return err
	}
	a, err := w.agent(child)
	if err != nil {
		return err
	}
	if a.Runtime != token || token == "" || a.Stopped {
		return errors.New("stale native host runtime")
	}
	if delegated, err := s.isDelegatedChild(root, child); err != nil || !delegated {
		return errors.New("native host requires a reserved delegated child")
	}
	lock, err := lockFile(nodeLockPath(child))
	if err != nil {
		return err
	}
	defer lock.Close()
	data, err := os.ReadFile(payload)
	if err != nil {
		return err
	}
	var request delegationHostLaunch
	if err = json.Unmarshal(data, &request); err != nil {
		return err
	}
	if request.Runtime != token || request.Cwd != a.Cwd || payload != filepath.Join(runtimeDir(), request.Job+".native-launch.json") {
		return errors.New("native host launch payload mismatch")
	}
	cmd := exec.Command(request.Command, request.Args...)
	cmd.Dir = a.Cwd
	for key, value := range request.Env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	cmd.Env = append(cmd.Env, "WT_ROOT_ID="+root, "WT_AGENT_ID="+child, "WT_RUNTIME_ID="+token, "WT_DB="+dbPath(), "WT_STATUS_DIR="+stateDir(), "WT_STATE="+runtimeBinary())
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err = cmd.Start(); err != nil {
		return err
	}
	if err = os.WriteFile(payload+".pid", []byte(fmt.Sprint(cmd.Process.Pid)), 0600); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	return cmd.Wait()
}

func (s *Store) validateDelegationHostCommand(job DelegationJob, request delegationHostLaunch) error {
	if len(request.Args) < 3 || filepath.Base(request.Command) != "node" || filepath.Base(request.Args[len(request.Args)-2]) != "subagent-runner.ts" {
		return errors.New("native host must execute the package-owned Node runner")
	}
	file, err := os.Open(request.Args[len(request.Args)-1])
	if err != nil {
		return err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 512*1024+1))
	if err != nil || len(data) > 512*1024 {
		return errors.New("invalid native runner config bound")
	}
	var config map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err = decoder.Decode(&config); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("native runner config must contain one JSON object")
	}
	binding, ok := config["nativeExecution"].(map[string]any)
	if !ok || binding["jobId"] != job.ID || binding["turnId"] != request.Turn || binding["configDigest"] != job.ContractDigest || binding["provider"] != request.Owner.Provider || binding["ownerSessionId"] != request.Owner.OwnerSessionID || binding["parentSessionId"] != request.Owner.ParentNativeID {
		return errors.New("native runner binding differs from its WT reservation")
	}
	delete(config, "nativeExecution")
	delete(config, "runnerProcessInstanceId")
	delete(config, "launchBarrierToken")
	var admissionJSON string
	if err = s.db.QueryRow(`SELECT admission FROM delegation_jobs WHERE id=?`, job.ID).Scan(&admissionJSON); err != nil {
		return err
	}
	var admission DelegationAdmission
	if err = json.Unmarshal([]byte(admissionJSON), &admission); err != nil {
		return err
	}
	var contract map[string]any
	decoder = json.NewDecoder(bytes.NewReader(admission.Contract))
	decoder.UseNumber()
	if err = decoder.Decode(&contract); err != nil {
		return err
	}
	if !reflect.DeepEqual(config, contract) {
		return errors.New("native runner changed its admitted package contract")
	}
	return nil
}
