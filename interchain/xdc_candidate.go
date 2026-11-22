package interchain

// XDCMainnetCandidate pins the deployed V5 registries and RPC policy.
func XDCMainnetCandidate() ChainPolicy {
	return ChainPolicy{
		Name: "xdc", ChainID: 50, Domain: 50,
		Finality:        FinalityFinalized,
		SignatureScheme: "ecdsa",
		VerifierFormat:  "ecdsa-secp256k1",
		// These endpoints returned the canonical historical receipt, logs and
		// block-pinned source fee. Count public-rpc.com conservatively in the
		// XinFin group until independent upstream operation is established.
		RPCURLs:                []string{"https://earpc.xinfin.network", "https://rpc.ankr.com/xdc", "https://xdc.public-rpc.com"},
		IndependentOperatorIDs: []string{"xinfin", "ankr", "xinfin"},
		// xgr-interchain main recorded ValidatorRegistry receipt 65a27764…
		// and Factory-created SourceRegistry receipt 00c6f791… at block 109627114.
		Registry:              "0x0334b4c9b1307fce68f25e72b65ab32dcc83343b",
		SourceRegistry:        "0x6d0c5859a80a0927b3e43bf56e532f9f16a13035",
		SourceActivationBlock: 109627114,
	}
}
