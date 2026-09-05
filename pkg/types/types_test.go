package types

import (
	"testing"
)

func TestCategorizeNode(t *testing.T) {
	tests := []struct {
		name     string
		rate     float64
		expected NodeCategory
	}{
		{"perfect", 1.0, NodeCategoryGood},
		{"high", 0.8, NodeCategoryGood},
		{"good_threshold", 0.76, NodeCategoryGood},
		{"okay_high", 0.75, NodeCategoryOkay},
		{"okay_mid", 0.6, NodeCategoryOkay},
		{"okay_threshold", 0.51, NodeCategoryOkay},
		{"bad_high", 0.50, NodeCategoryBad},
		{"bad_low", 0.3, NodeCategoryBad},
		{"zero", 0.0, NodeCategoryBad},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CategorizeNode(tt.rate)
			if got != tt.expected {
				t.Errorf("CategorizeNode(%f) = %v, want %v", tt.rate, got, tt.expected)
			}
		})
	}
}

func TestNodeCategory_String(t *testing.T) {
	tests := []struct {
		cat      NodeCategory
		expected string
	}{
		{NodeCategoryGood, "good"},
		{NodeCategoryOkay, "okay"},
		{NodeCategoryBad, "bad"},
	}

	for _, tt := range tests {
		if got := tt.cat.String(); got != tt.expected {
			t.Errorf("String() = %s, want %s", got, tt.expected)
		}
	}
}

func TestNodeCategory_Constants(t *testing.T) {
	if NodeCategoryGood != "good" {
		t.Errorf("NodeCategoryGood = %s, want good", NodeCategoryGood)
	}
	if NodeCategoryOkay != "okay" {
		t.Errorf("NodeCategoryOkay = %s, want okay", NodeCategoryOkay)
	}
	if NodeCategoryBad != "bad" {
		t.Errorf("NodeCategoryBad = %s, want bad", NodeCategoryBad)
	}
}

func TestStateKeyConstants(t *testing.T) {
	if StateKeyCurrentNodeID == "" {
		t.Error("StateKeyCurrentNodeID should not be empty")
	}
	if StateKeyLockedNodeID == "" {
		t.Error("StateKeyLockedNodeID should not be empty")
	}
	if StateKeyLastCheckTime == "" {
		t.Error("StateKeyLastCheckTime should not be empty")
	}

	// Ensure they're unique
	keys := map[string]bool{
		StateKeyCurrentNodeID: true,
		StateKeyLockedNodeID:  true,
		StateKeyLastCheckTime: true,
	}
	if len(keys) != 3 {
		t.Error("state keys are not unique")
	}
}
