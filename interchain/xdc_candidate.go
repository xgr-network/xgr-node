package interchain

// XDCMainnetCandidate is a NON-ACTIVATING proposal. XDC is EVM chainId/domain
// 50, but the Registry/SourceRegistry/ISM and finality witnesses must be
// independently verified before it can enter compiledChains.
// This method NEVER returns an active policy and never changes BLS routes.
func XDCMainnetCandidate() ChainPolicy {
 return ChainPolicy{
  Name:"xdc", ChainID:50, Domain:50,
  SignatureScheme:"ecdsa",
  VerifierFormat:"ecdsa-secp256k1",
  // RPC endpoints and source finality policy are intentionally withheld
  // until two independent mainnet witnesses have been proven.
  Registry:"", SourceRegistry:"", SourceActivationBlock:0,
 }
}
