package framework

// WaitForServersToSealExcept waits for a target block height on all IBFT servers
// except indexes explicitly excluded. Useful for jailing/slashing tests where one
// or more validators are intentionally kept offline.
func WaitForServersToSealExcept(
	manager *IBFTServersManager,
	desiredHeight uint64,
	excluded ...int,
) []error {
	if manager == nil {
		return nil
	}

	servers := manager.ActiveServers(excluded...)
	if len(servers) == 0 {
		return nil
	}

	return WaitForServersToSeal(servers, desiredHeight)
}
