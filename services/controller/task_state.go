package main

import (
	"errors"
	"regexp"
)

type taskState string

const (
	taskQueued          taskState = "QUEUED"
	taskReady           taskState = "READY"
	taskAwaitApproval   taskState = "AWAIT_APPROVAL"
	taskDeveloping      taskState = "DEVELOPING"
	taskWaitCI          taskState = "WAIT_CI"
	taskCIFailed        taskState = "CI_FAILED"
	taskStagePassed     taskState = "STAGE_PASSED"
	taskHandoffRequired taskState = "HANDOFF_REQUIRED"
	taskHandoffReady    taskState = "HANDOFF_READY"
	taskPaused          taskState = "PAUSED"
	taskOffline         taskState = "OFFLINE"
	taskNeedHuman       taskState = "NEED_HUMAN"
	taskCompleted       taskState = "COMPLETED"
	taskStopped         taskState = "STOPPED"
)

type taskAction string

const (
	actionReady           taskAction = "ready"
	actionPrepare         taskAction = "prepare"
	actionConfirmSent     taskAction = "confirm_sent"
	actionObserveCommit   taskAction = "observe_commit"
	actionCIPass          taskAction = "ci_pass"
	actionCIFail          taskAction = "ci_fail"
	actionRepair          taskAction = "repair"
	actionNext            taskAction = "next_stage"
	actionComplete        taskAction = "complete"
	actionRequireHandoff  taskAction = "require_handoff"
	actionVerifyHandoff   taskAction = "verify_handoff"
	actionResumeHandoff   taskAction = "resume_handoff"
	actionPause           taskAction = "pause"
	actionOffline         taskAction = "offline"
	actionResume          taskAction = "resume"
	actionNeedHuman       taskAction = "need_human"
	actionResolve         taskAction = "resolve"
	actionStop            taskAction = "stop"
)

var (
	errTaskTransition = errors.New("invalid task transition or missing evidence")
	errTaskRevision   = errors.New("task revision conflict")
	taskSHA           = regexp.MustCompile("^[0-9a-f]{40}$")
)

// taskSnapshot is a pure domain model. Persistence and the authenticated
// command/evidence adapters are deliberately separate from this state machine.
type taskSnapshot struct {
	State      taskState
	Suspended  taskState
	Revision   uint64
	Stage      int
	StageCount int
	Repairs    int
	MaxRepairs int
	CodeSHA    string
}

// taskEvidence is trusted internal input, NOT a JSON request payload.
// The caller must verify authorization, lease, CI and Git facts before
// constructing it. No transition itself sends a browser instruction.
type taskEvidence struct {
	ReadyToRun      bool
	ManualSent      bool
	VerifiedCommit  bool
	CodeSHA         string
	CIComplete      bool
	CIAllPassed     bool
	Acceptance      bool
	Reconciled      bool
	VerifiedHandoff bool
	ManualResume    bool
	FinalAccepted   bool
}

func newTaskSnapshot(stageCount, maxRepairs int) (taskSnapshot, error) {
	if stageCount < 1 || stageCount > 100 || maxRepairs < 0 || maxRepairs > 20 {
		return taskSnapshot{}, errTaskTransition
	}
	return taskSnapshot{State: taskQueued, StageCount: stageCount, MaxRepairs: maxRepairs}, nil
}

func (s taskSnapshot) terminal() bool {
	return s.State == taskStopped || s.State == taskCompleted
}

func (s taskSnapshot) valid() bool {
	if s.StageCount < 1 || s.StageCount > 100 || s.Stage < 0 || s.Stage >= s.StageCount || s.MaxRepairs < 0 || s.MaxRepairs > 20 || s.Repairs < 0 || s.Repairs > s.MaxRepairs {
		return false
	}
	switch s.State {
	case taskQueued, taskReady, taskAwaitApproval, taskDeveloping, taskCIFailed, taskHandoffRequired, taskHandoffReady, taskPaused, taskOffline, taskNeedHuman, taskStopped:
		return true
	case taskWaitCI, taskStagePassed, taskCompleted:
		return taskSHA.MatchString(s.CodeSHA)
	default:
		return false
	}
}

// advanceTask returns the unchanged snapshot on any rejected operation.
// Revision comparison is required even for stop; the store must enforce the
// same comparison atomically when writing the returned snapshot.
func advanceTask(s taskSnapshot, expected uint64, action taskAction, e taskEvidence) (taskSnapshot, error) {
	if expected != s.Revision {
		return s, errTaskRevision
	}
	if !s.valid() || s.terminal() || s.Revision == ^uint64(0) {
		return s, errTaskTransition
	}
	next := s
	ok := false
	switch action {
	case actionReady:
		ok = s.State == taskQueued && e.ReadyToRun
		next.State = taskReady
	case actionPrepare:
		ok = s.State == taskReady && e.ReadyToRun
		next.State = taskAwaitApproval
	case actionConfirmSent:
		ok = s.State == taskAwaitApproval && e.ManualSent && e.ReadyToRun
		next.State = taskDeveloping
	case actionObserveCommit:
		ok = s.State == taskDeveloping && e.VerifiedCommit && taskSHA.MatchString(e.CodeSHA) && e.CodeSHA != s.CodeSHA
		next.State, next.CodeSHA = taskWaitCI, e.CodeSHA
	case actionCIPass:
		ok = s.State == taskWaitCI && e.CodeSHA == s.CodeSHA && e.CIComplete && e.CIAllPassed && e.Acceptance
		next.State = taskStagePassed
	case actionCIFail:
		ok = s.State == taskWaitCI && e.CodeSHA == s.CodeSHA && e.CIComplete && !e.CIAllPassed
		next.State = taskCIFailed
	case actionRepair:
		ok = s.State == taskCIFailed && e.ReadyToRun
		if s.Repairs >= s.MaxRepairs {
			next.State = taskNeedHuman
			next.Suspended = taskCIFailed
		} else {
			next.State = taskAwaitApproval
			next.Repairs++
		}
	case actionNext:
		ok = s.State == taskStagePassed && s.Stage+1 < s.StageCount
		next.Stage++
		next.Repairs = 0
		next.State = taskQueued
	case actionComplete:
		ok = s.State == taskStagePassed && s.Stage+1 == s.StageCount && e.FinalAccepted
		next.State = taskCompleted
	case actionRequireHandoff:
		ok = s.State == taskDeveloping || s.State == taskStagePassed
		next.Suspended = s.State
		next.State = taskHandoffRequired
	case actionVerifyHandoff:
		ok = s.State == taskHandoffRequired && e.VerifiedHandoff && e.CodeSHA == s.CodeSHA && taskSHA.MatchString(e.CodeSHA)
		next.State = taskHandoffReady
	case actionResumeHandoff:
		ok = s.State == taskHandoffReady && e.ManualResume && e.Reconciled
		if s.Suspended == taskStagePassed {
			next.State = taskStagePassed
		} else if s.Suspended == taskDeveloping {
			next.State = taskAwaitApproval
		} else {
			ok = false
		}
		next.Suspended = ""
	case actionPause, actionOffline, actionNeedHuman:
		ok = s.State != taskPaused && s.State != taskOffline && s.State != taskNeedHuman && s.State != taskHandoffRequired && s.State != taskHandoffReady
		next.Suspended = s.State
		switch action {
		case actionPause:
			next.State = taskPaused
		case actionOffline:
			next.State = taskOffline
		case actionNeedHuman:
			next.State = taskNeedHuman
		}
	case actionResume, actionResolve:
		ok = e.Reconciled && e.ManualResume &&
			((action == actionResume && (s.State == taskPaused || s.State == taskOffline)) ||
				(action == actionResolve && s.State == taskNeedHuman))
		switch s.Suspended {
		case taskQueued, taskReady, taskAwaitApproval, taskWaitCI, taskCIFailed, taskStagePassed:
			next.State = s.Suspended
		case taskDeveloping:
			// Unknown delivery outcome must not cause a resend.
			next.State = taskDeveloping
		default:
			ok = false
		}
		next.Suspended = ""
	case actionStop:
		ok = true
		next.State, next.Suspended = taskStopped, ""
	}
	if !ok || !next.valid() {
		return s, errTaskTransition
	}
	next.Revision++
	return next, nil
}
