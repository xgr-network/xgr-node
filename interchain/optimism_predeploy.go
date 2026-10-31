package interchain

// ProposedOptimismPolicy is a non-executable OP Mainnet v3.4.3 planning
// artifact, intentionally NOT in compiledChains or ActivePolicies.
// A policy cannot authorize validator reads/signing until real, immutable
// deployment receipts pin its ValidatorRegistry, SourceRegistry and the
// RPC-native SourceRegistry creation block. Do not replace these zeros by
// invented addresses or implicitly enable the chain.
//
// Every RPC endpoint must be independently checked for chain ID 10,
// EIP-2537 positive/negative vectors and supported "safe" finality.
// Endpoint/operator independence is enforced by validatePolicy only
// after the reviewed chain is moved into compiledChains.
func ProposedOptimismPolicy() ChainPolicy {
 return ChainPolicy{
  Name:"optimism",ChainID:10,Domain:10,Finality:FinalitySafe,
  VerifierFormat:"eip2537",
  RPCURLs:[]string{
   "https://mainnet.optimism.io",
   "https://optimism-rpc.publicnode.com",
   "https://optimism.drpc.org",
   "https://optimism.gateway.tenderly.co",
  },
  IndependentOperatorIDs:[]string{"optimism-official","publicnode","drpc","tenderly"},
  Registry:ZeroRegistryAddress,
  SourceRegistry:ZeroRegistryAddress,
  SourceActivationBlock:0,
 }
}
