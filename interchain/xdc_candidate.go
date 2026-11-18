package interchain

// XDCMainnetCandidate is an UNBOUND, NON-ACTIVATING V5 placeholder.
// The previously receipt-pinned V4 addresses remain in xdc_deployments.go
// for historical audit ONLY and MUST NOT be used for ECDSA validator signing.
// V5 constructor and SourceRegistry addresses are not deployed or approved.
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
