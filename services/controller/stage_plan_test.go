package main

import (
	"strings"
	"testing"
)

func TestStagePlanValidation(t *testing.T) {
	first := stageCreate{ID: "S1", Weight: 1, Instruction: "Implement", Acceptance: []string{"unit"}}
	second := stageCreate{ID: "S2", Weight: 2, Instruction: "Verify", Dependencies: []string{"S1"}, Acceptance: []string{"integration"}}
	if !validStagePlan([]stageCreate{first, second}) {
		t.Fatal("ordered plan rejected")
	}
	for _, bad := range []stageCreate{
		{ID: "S2", Weight: 2, Instruction: "Verify", Dependencies: []string{"S2"}, Acceptance: []string{"unit"}},
		{ID: "S2", Weight: 2, Instruction: "Verify", Dependencies: []string{"missing"}, Acceptance: []string{"unit"}},
		{ID: "S2", Weight: 2, Instruction: "Verify", Dependencies: []string{"S1", "S1"}, Acceptance: []string{"unit"}},
		{ID: "S2", Weight: 2, Instruction: "Verify", Acceptance: []string{"unit", "unit"}},
		{ID: "S2", Weight: 2, Instruction: "Verify", Acceptance: []string{" "}},
		{ID: "S2", Weight: 2, Instruction: "Verify", Acceptance: []string{" unit"}},
		{ID: "S2", Weight: 2, Instruction: strings.Repeat("x", 8193), Acceptance: []string{"unit"}},
		{ID: "S2", Weight: 2, Instruction: " ", Acceptance: []string{"unit"}},
		{ID: "S2", Weight: 2, Instruction: "Verify", Acceptance: []string{}},
	} {
		if validStagePlan([]stageCreate{first, bad}) {
			t.Fatal("invalid plan accepted", bad.ID, bad.Dependencies, bad.Acceptance)
		}
	}
	if validStagePlan([]stageCreate{second, first}) {
		t.Fatal("forward dependency accepted")
	}
}
