package rooms

import (
	"strings"
	"testing"
)

// The contract is the client's only lever on how members address the room:
// it must keep all five points, name the owner, and stay small enough to ride
// a room message without crowding it.
func TestDiscussionBlock_StatesTheContract(t *testing.T) {
	block := DiscussionBlock()

	points := 0
	for _, line := range strings.Split(block, "\n") {
		if len(line) >= 2 && line[0] >= '1' && line[0] <= '5' && line[1] == '.' {
			points++
		}
	}
	if points != 5 {
		t.Fatalf("contract has %d numbered points, want 5:\n%s", points, block)
	}
	for _, want := range []string{"Kamil", "JEDNO", "bez plików i bez HANDOFF"} {
		if !strings.Contains(block, want) {
			t.Errorf("contract is missing %q:\n%s", want, block)
		}
	}
	if got := len(block); got >= 1200 {
		t.Errorf("contract is %d bytes, want < 1200", got)
	}
}
