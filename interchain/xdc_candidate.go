package interchain

// XDCMainnetCandidate is an UNBOUND, NON-ACTIVATING V5 placeholder.
// The destination contract binding is unset until approved V5 deployment.
func XDCMainnetCandidate() ChainPolicy {
 return ChainPolicy{
  Name:"xdc",ChainID:50,Domain:50,
  Finality:FinalityFinalized,
  SignatureScheme:"ecdsa",
  VerifierFormat:"ecdsa-secp256k1",
  RPCURLs:[]string{"https://erpc.xinfin.network","https://rpc.xdc.blocksscan.io","https://rpc.ankr.com/xdc"},
  IndependentOperatorIDs:[]string{"xinfin","blocksscan","ankr"},
  Registry:ZeroRegistryAddress,
  SourceRegistry:ZeroRegistryAddress,
  SourceActivationBlock:0,
 }
}
