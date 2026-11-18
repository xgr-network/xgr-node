package interchain

// XDCMainnetCandidate contains receipt-pinned XDC Mainnet deployment bindings.
// This is a NON-ACTIVATING policy: ECDSA validator signing and XDC routes
// remain disabled until independently audited and explicitly released.
func XDCMainnetCandidate() ChainPolicy {
 return ChainPolicy{
  Name:"xdc",ChainID:50,Domain:50,
  Finality:FinalityFinalized,
  SignatureScheme:"ecdsa",
  VerifierFormat:"ecdsa-secp256k1",
  RPCURLs:[]string{"https://erpc.xinfin.network","https://rpc.xdc.blocksscan.io","https://rpc.ankr.com/xdc"},
  IndependentOperatorIDs:[]string{"xinfin","blocksscan","ankr"},
  Registry:"0x70f5752326735b31641f21d174ba035e904db93c",
  SourceRegistry:"0x14d5d33a65a39f96665ed631b04fecf357bcb576",
  SourceActivationBlock:109507718,
 }
}
