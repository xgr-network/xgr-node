// Package interchain implements XGR's optional interchain validator protocol.
//
// It is deliberately isolated from the XGR PoS consensus quorum. Eligibility
// originates from the XGR staking validator identity, while interchain votes
// use an unweighted two-thirds quorum and domain-separated BLS signatures.
package interchain
