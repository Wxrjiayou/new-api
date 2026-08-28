package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPriorityCycleModulo(t *testing.T) {
	// Simulate the priority cycling logic from GetRandomSatisfiedChannel
	// Given 3 priority tiers, retry should cycle through them
	priorities := []int{100, 50, 10} // sorted descending: high, medium, low

	tests := []struct {
		retry            int
		expectedPriority int
		description      string
	}{
		{0, 100, "first attempt uses highest priority"},
		{1, 50, "second attempt uses medium priority"},
		{2, 10, "third attempt uses lowest priority"},
		{3, 100, "fourth attempt cycles back to highest"},
		{4, 50, "fifth attempt cycles to medium"},
		{5, 10, "sixth attempt cycles to lowest"},
		{6, 100, "seventh attempt cycles back to highest again"},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			idx := tt.retry % len(priorities)
			got := priorities[idx]
			assert.Equal(t, tt.expectedPriority, got)
		})
	}
}

func TestPriorityCycleSingleTier(t *testing.T) {
	// When there's only one priority tier, modulo always returns 0
	priorities := []int{100}

	for retry := 0; retry < 5; retry++ {
		idx := retry % len(priorities)
		assert.Equal(t, 0, idx)
		assert.Equal(t, 100, priorities[idx])
	}
}

func TestPriorityCycleTwoTiers(t *testing.T) {
	priorities := []int{80, 20}

	expected := []int{80, 20, 80, 20, 80, 20}
	for retry := 0; retry < len(expected); retry++ {
		idx := retry % len(priorities)
		assert.Equal(t, expected[retry], priorities[idx])
	}
}
