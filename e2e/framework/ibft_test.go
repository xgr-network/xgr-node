package framework

import "testing"

func TestIBFTServersManagerActiveServers(t *testing.T) {
	t.Parallel()

	manager := &IBFTServersManager{
		servers: []*TestServer{{}, {}, {}, {}},
	}

	active := manager.ActiveServers(1, 3)
	if len(active) != 2 {
		t.Fatalf("expected 2 active servers, got %d", len(active))
	}
}

func TestIBFTServersManagerStopServerOutOfRange(t *testing.T) {
	t.Parallel()

	manager := &IBFTServersManager{servers: []*TestServer{}}
	manager.StopServer(10)
}
