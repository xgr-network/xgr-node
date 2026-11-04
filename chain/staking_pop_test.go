package chain

import "testing"

func TestStakingPoPHasOneExplicitActivation(t *testing.T) {
	f := Forks{}
	if f.At(^uint64(0)).StakingBLSPoP || AllForksEnabled.At(0).StakingBLSPoP {
		t.Fatal("PoP fork must have no implicit height")
	}
	f.SetFork(StakingBLSPoP, NewFork(100))
	if f.At(99).StakingBLSPoP || !f.At(100).StakingBLSPoP || !f.At(101).StakingBLSPoP {
		t.Fatal("incorrect inclusive activation boundary")
	}
	if len(f) != 1 {
		t.Fatal("more than one activation gate")
	}
}
