package main

import (
	"errors"
	"strings"
	"testing"
)

var testCodeSHA = strings.Repeat("a", 40)

func stepTask(t *testing.T, s taskSnapshot, action taskAction, e taskEvidence, want taskState) taskSnapshot {
	t.Helper()
	got, err := advanceTask(s, s.Revision, action, e)
	if err != nil || got.State != want || got.Revision != s.Revision+1 {
		t.Fatalf("%s from %s: got %+v, err=%v, want %s", action, s.State, got, err, want)
	}
	return got
}

func rejectTask(t *testing.T, s taskSnapshot, action taskAction, e taskEvidence) {
	t.Helper()
	got, err := advanceTask(s, s.Revision, action, e)
	if !errors.Is(err, errTaskTransition) || got != s {
		t.Fatalf("accepted invalid %s from %s: %+v %v", action, s.State, got, err)
	}
}

func waitingTask(t *testing.T, stages, repairs int) taskSnapshot {
	t.Helper()
	s, err := newTaskSnapshot(stages, repairs)
	if err != nil {
		t.Fatal(err)
	}
	rejectTask(t, s, actionReady, taskEvidence{})
	s = stepTask(t, s, actionReady, taskEvidence{ReadyToRun: true}, taskReady)
	rejectTask(t, s, actionPrepare, taskEvidence{})
	s = stepTask(t, s, actionPrepare, taskEvidence{ReadyToRun: true}, taskAwaitApproval)
	rejectTask(t, s, actionConfirmSent, taskEvidence{ReadyToRun: true})
	s = stepTask(t, s, actionConfirmSent, taskEvidence{ManualSent: true, ReadyToRun: true}, taskDeveloping)
	rejectTask(t, s, actionObserveCommit, taskEvidence{CodeSHA: testCodeSHA})
	rejectTask(t, s, actionObserveCommit, taskEvidence{CodeSHA: "bad", VerifiedCommit: true})
	return stepTask(t, s, actionObserveCommit, taskEvidence{CodeSHA: testCodeSHA, VerifiedCommit: true}, taskWaitCI)
}

func TestTaskCIAndFinalApproval(t *testing.T) {
	s := waitingTask(t, 2, 1)
	pass := taskEvidence{CodeSHA: testCodeSHA, CIComplete: true, CIAllPassed: true, Acceptance: true}
	for _, e := range []taskEvidence{
		{CodeSHA: strings.Repeat("b", 40), CIComplete: true, CIAllPassed: true, Acceptance: true},
		{CodeSHA: testCodeSHA, CIAllPassed: true, Acceptance: true},
		{CodeSHA: testCodeSHA, CIComplete: true, CIAllPassed: true},
		{CodeSHA: testCodeSHA, CIComplete: true, Acceptance: true},
	} {
		rejectTask(t, s, actionCIPass, e)
	}
	rejectTask(t, s, actionComplete, taskEvidence{FinalAccepted: true})
	s = stepTask(t, s, actionCIPass, pass, taskStagePassed)
	rejectTask(t, s, actionComplete, taskEvidence{FinalAccepted: true})
	s = stepTask(t, s, actionNext, taskEvidence{}, taskQueued)
	if s.Stage != 1 || s.Repairs != 0 {
		t.Fatal(s)
	}
	s = stepTask(t, s, actionReady, taskEvidence{ReadyToRun: true}, taskReady)
	s = stepTask(t, s, actionPrepare, taskEvidence{ReadyToRun: true}, taskAwaitApproval)
	s = stepTask(t, s, actionConfirmSent, taskEvidence{ManualSent: true, ReadyToRun: true}, taskDeveloping)
	rejectTask(t, s, actionObserveCommit, taskEvidence{CodeSHA: testCodeSHA, VerifiedCommit: true})
	pass.CodeSHA = strings.Repeat("c", 40)
	s = stepTask(t, s, actionObserveCommit, taskEvidence{CodeSHA: pass.CodeSHA, VerifiedCommit: true}, taskWaitCI)
	s = stepTask(t, s, actionCIPass, pass, taskStagePassed)
	rejectTask(t, s, actionNext, taskEvidence{})
	rejectTask(t, s, actionComplete, taskEvidence{})
	s = stepTask(t, s, actionComplete, taskEvidence{FinalAccepted: true}, taskCompleted)
	rejectTask(t, s, actionStop, taskEvidence{})
}

func TestTaskRepairAndStop(t *testing.T) {
	s := waitingTask(t, 1, 1)
	fail := taskEvidence{CodeSHA: testCodeSHA, CIComplete: true}
	rejectTask(t, s, actionCIFail, taskEvidence{CodeSHA: testCodeSHA})
	s = stepTask(t, s, actionCIFail, fail, taskCIFailed)
	s = stepTask(t, s, actionRepair, taskEvidence{ReadyToRun: true}, taskAwaitApproval)
	if s.Repairs != 1 {
		t.Fatal(s)
	}
	s = stepTask(t, s, actionConfirmSent, taskEvidence{ManualSent: true, ReadyToRun: true}, taskDeveloping)
	fail.CodeSHA = strings.Repeat("d", 40)
	s = stepTask(t, s, actionObserveCommit, taskEvidence{CodeSHA: fail.CodeSHA, VerifiedCommit: true}, taskWaitCI)
	s = stepTask(t, s, actionCIFail, fail, taskCIFailed)
	s = stepTask(t, s, actionRepair, taskEvidence{ReadyToRun: true}, taskNeedHuman)
	if s.Repairs != 1 {
		t.Fatal(s)
	}
	rejectTask(t, s, actionResolve, taskEvidence{})
	s = stepTask(t, s, actionResolve, taskEvidence{Reconciled: true, ManualResume: true}, taskCIFailed)
	s = stepTask(t, s, actionStop, taskEvidence{}, taskStopped)
	for _, a := range []taskAction{actionReady, actionPrepare, actionConfirmSent, actionObserveCommit, actionCIPass, actionCIFail, actionRepair, actionNext, actionComplete, actionRequireHandoff, actionVerifyHandoff, actionResumeHandoff, actionPause, actionOffline, actionResume, actionNeedHuman, actionResolve, actionStop} {
		rejectTask(t, s, a, taskEvidence{ReadyToRun: true, ManualResume: true, Reconciled: true})
	}
}

func TestTaskSuspensionAndHandoff(t *testing.T) {
	s := waitingTask(t, 1, 0)
	for _, a := range []taskAction{actionPause, actionOffline, actionNeedHuman} {
		want, resume := taskPaused, actionResume
		if a == actionOffline {
			want = taskOffline
		} else if a == actionNeedHuman {
			want, resume = taskNeedHuman, actionResolve
		}
		s = stepTask(t, s, a, taskEvidence{}, want)
		rejectTask(t, s, resume, taskEvidence{Reconciled: true})
		rejectTask(t, s, actionConfirmSent, taskEvidence{ManualSent: true, ReadyToRun: true})
		s = stepTask(t, s, resume, taskEvidence{ManualResume: true, Reconciled: true}, taskWaitCI)
	}
	s = stepTask(t, s, actionCIPass, taskEvidence{CodeSHA: testCodeSHA, CIComplete: true, CIAllPassed: true, Acceptance: true}, taskStagePassed)
	s = stepTask(t, s, actionRequireHandoff, taskEvidence{}, taskHandoffRequired)
	rejectTask(t, s, actionVerifyHandoff, taskEvidence{VerifiedHandoff: true, CodeSHA: strings.Repeat("e", 40)})
	s = stepTask(t, s, actionVerifyHandoff, taskEvidence{VerifiedHandoff: true, CodeSHA: testCodeSHA}, taskHandoffReady)
	rejectTask(t, s, actionResumeHandoff, taskEvidence{Reconciled: true})
	stepTask(t, s, actionResumeHandoff, taskEvidence{Reconciled: true, ManualResume: true}, taskStagePassed)
}

func TestTaskRevisionAndInvalidSnapshots(t *testing.T) {
	for _, args := range [][2]int{{0, 0}, {101, 0}, {1, -1}, {1, 21}} {
		if _, err := newTaskSnapshot(args[0], args[1]); !errors.Is(err, errTaskTransition) {
			t.Fatal(args, err)
		}
	}
	s := waitingTask(t, 1, 0)
	got, err := advanceTask(s, s.Revision-1, actionStop, taskEvidence{})
	if got != s || !errors.Is(err, errTaskRevision) {
		t.Fatal(got, err)
	}
	rejectTask(t, s, "unknown", taskEvidence{})
	s.Revision = ^uint64(0)
	rejectTask(t, s, actionStop, taskEvidence{})
	rejectTask(t, taskSnapshot{}, actionStop, taskEvidence{})
	s = taskSnapshot{State: "unknown", StageCount: 1}
	rejectTask(t, s, actionStop, taskEvidence{})
}

func TestTaskSuspendedDevelopingDoesNotResend(t *testing.T) {
	s := waitingTask(t, 1, 1)
	s = stepTask(t, s, actionCIFail, taskEvidence{CodeSHA: testCodeSHA, CIComplete: true}, taskCIFailed)
	s = stepTask(t, s, actionRepair, taskEvidence{ReadyToRun: true}, taskAwaitApproval)
	s = stepTask(t, s, actionConfirmSent, taskEvidence{ManualSent: true, ReadyToRun: true}, taskDeveloping)
	s = stepTask(t, s, actionOffline, taskEvidence{}, taskOffline)
	s = stepTask(t, s, actionResume, taskEvidence{ManualResume: true, Reconciled: true}, taskDeveloping)
	rejectTask(t, s, actionConfirmSent, taskEvidence{ManualSent: true, ReadyToRun: true})
	s = stepTask(t, s, actionRequireHandoff, taskEvidence{}, taskHandoffRequired)
	s = stepTask(t, s, actionVerifyHandoff, taskEvidence{CodeSHA: testCodeSHA, VerifiedHandoff: true}, taskHandoffReady)
	stepTask(t, s, actionResumeHandoff, taskEvidence{ManualResume: true, Reconciled: true}, taskAwaitApproval)
}
