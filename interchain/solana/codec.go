// Package solana provides a read-only, non-signing SVM adapter and a
// candidate Solana-facing Direct-BLS codec for XITA V315. EVM paths unchanged.
package solana

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"

	"golang.org/x/crypto/sha3"
)

const XGRDomain uint32 = 1643
const SolanaDomain uint32 = 1399811149
const assetScope = "XITA_SOLANA_ASSET_V1"
const routeScope = "XITA_SOLANA_ROUTE_V1"
const directScope = "XITA_SOLANA_DIRECT_MESSAGE_V1"

// SolanaGenesisBytes is the canonical mainnet-beta genesis hash decoded from
// Base58; it is NOT an EVM chainID.
var SolanaGenesisBytes = [32]byte{
	0x45, 0x29, 0x69, 0x98, 0xa6, 0xf8, 0xe2, 0xa7,
	0x84, 0xdb, 0x5d, 0x9f, 0x95, 0xe1, 0x8f, 0xc2,
	0x3f, 0x70, 0x44, 0x1a, 0x10, 0x39, 0x44, 0x68,
	0x01, 0x08, 0x98, 0x79, 0xb0, 0x8c, 0x7e, 0xf0,
}

var codecErr = errors.New("invalid Solana XITA codec data")

func hash(parts ...[]byte) [32]byte {
	h := sha3.NewLegacyKeccak256()
	for _, p := range parts {
		_, _ = h.Write(p)
	}
	var out [32]byte
	h.Sum(out[:0])
	return out
}
func uintBE(x uint64, n int) []byte {
	b := make([]byte, n)
	for i := n - 1; i >= 0; i-- {
		b[i] = byte(x)
		x >>= 8
	}
	return b
}
func abiUint(x uint64) []byte {
	b := make([]byte, 32)
	copy(b[24:], uintBE(x, 8))
	return b
}
func uint256(x *big.Int) ([]byte, error) {
	if x == nil || x.Sign() <= 0 || x.BitLen() > 256 {
		return nil, codecErr
	}
	b := make([]byte, 32)
	x.FillBytes(b)
	return b, nil
}
func nonZero(b [32]byte) bool { return b != ([32]byte{}) }
func isEvmAddress32(b [32]byte) bool {
	return nonZero(b) && bytes.Equal(b[:12], make([]byte, 12))
}
func validateRouter(domain uint32, id [32]byte) error {
	if !nonZero(id) || (domain == XGRDomain && !isEvmAddress32(id)) {
		return codecErr
	}
	return nil
}
func validateDomains(src, dst uint32) error {
	if !((src == XGRDomain && dst == SolanaDomain) ||
		(src == SolanaDomain && dst == XGRDomain)) {
		return codecErr
	}
	return nil
}

// EVMAddress32 is the unique right-aligned 20-byte EVM address embedding.
func EVMAddress32(addr [20]byte) ([32]byte, error) {
	if addr == ([20]byte{}) {
		return [32]byte{}, codecErr
	}
	var out [32]byte
	copy(out[12:], addr[:])
	return out, nil
}

// DecodeSolanaPubkey decodes exactly a 32-byte Base58 Solana public key.
// The zero key can be used as the native SOL mint sentinel only.
func DecodeSolanaPubkey(encoded string) ([32]byte, error) {
	var out [32]byte
	if len(encoded) < 32 || len(encoded) > 44 {
		return out, codecErr
	}
	const alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
	acc := new(big.Int)
	mul := big.NewInt(58)
	for _, c := range encoded {
		index := bytes.IndexByte([]byte(alphabet), byte(c))
		if c > 127 || index < 0 {
			return out, codecErr
		}
		acc.Mul(acc, mul)
		acc.Add(acc, big.NewInt(int64(index)))
	}
	leading := 0
	for leading < len(encoded) && encoded[leading] == '1' {
		leading++
	}
	raw := append(make([]byte, leading), acc.Bytes()...)
	if len(raw) != 32 {
		return out, codecErr
	}
	copy(out[:], raw)
	return out, nil
}

// SolanaAssetID is only used for Solana-origin SOL/SPL assets. EVM-origin
// canonical asset IDs continue to use unchanged XGRILNProtocol V315.
func SolanaAssetID(mint [32]byte, kind byte) ([32]byte, error) {
	if (kind != 0 && kind != 1) || (kind == 0 && nonZero(mint)) ||
		(kind == 1 && !nonZero(mint)) {
		return [32]byte{}, codecErr
	}
	scope := hash([]byte(assetScope))
	kindWord := abiUint(uint64(kind))
	domainWord := abiUint(uint64(SolanaDomain))
	return hash(scope[:], domainWord, SolanaGenesisBytes[:], mint[:], kindWord), nil
}

// SolanaRouteID is domain-separated from every V315 EVM route, immutable and
// restricted to exactly one XGRChain endpoint.
func SolanaRouteID(assetID [32]byte, srcDomain, dstDomain uint32,
	srcRouter, dstRouter [32]byte) ([32]byte, error) {
	if err := validateDomains(srcDomain, dstDomain); err != nil {
		return [32]byte{}, err
	}
	if !nonZero(assetID) {
		return [32]byte{}, codecErr
	}
	if err := validateRouter(srcDomain, srcRouter); err != nil {
		return [32]byte{}, err
	}
	if err := validateRouter(dstDomain, dstRouter); err != nil {
		return [32]byte{}, err
	}
	scope := hash([]byte(routeScope))
	return hash(scope[:], assetID[:], abiUint(uint64(srcDomain)),
		abiUint(uint64(dstDomain)), srcRouter[:], dstRouter[:], SolanaGenesisBytes[:]), nil
}

// DirectAuthorization contains exact per-message fields. No validator may
// sign based on this codec alone: verified finalized source escrow, fees,
// route/program identity, current destination set and replay check are required.
type DirectAuthorization struct {
	SourceDomain      uint32
	DestinationDomain uint32
	AssetID           [32]byte
	RouteID           [32]byte
	SetID             uint64
	SourceHeight      uint64 // EVM block number or SVM finalized slot
	SourceRegistry    [32]byte
	Gateway           [32]byte
	SourceRouter      [32]byte
	SourceMailbox     [32]byte
	DestinationRouter [32]byte
	ValidatorFeeAtomic *big.Int // source-native atomic units (XGR wei or SOL lamports)
	MessageID         [32]byte
	Recipient         [32]byte
	AmountCanonical   *big.Int // original asset atomic units
	DestinationISM    [32]byte
	DestinationRegistry [32]byte
}

// DirectSigningBytes returns an exact 501-byte candidate preimage. It MUST
// not be connected to the validator BLS signing path without security review.
func DirectSigningBytes(a DirectAuthorization) ([]byte, error) {
	expected, err := SolanaRouteID(a.AssetID,a.SourceDomain,a.DestinationDomain,
		a.SourceRouter,a.DestinationRouter)
	if err != nil || expected != a.RouteID || a.SetID == 0 || a.SourceHeight == 0 {
		return nil, codecErr
	}
	for _, x := range [][32]byte{a.SourceRegistry,a.Gateway,a.SourceMailbox,
		a.MessageID,a.Recipient,a.DestinationISM,a.DestinationRegistry} {
		if !nonZero(x) {
			return nil, codecErr
		}
	}
	if a.SourceDomain == XGRDomain {
		for _, x := range [][32]byte{a.SourceRegistry,a.Gateway,a.SourceMailbox} {
			if !isEvmAddress32(x) {
				return nil, codecErr
			}
		}
	} else {
		for _, x := range [][32]byte{a.DestinationISM,a.DestinationRegistry,a.Recipient} {
			if !isEvmAddress32(x) {
				return nil, codecErr
			}
		}
	}
	fee, err := uint256(a.ValidatorFeeAtomic)
	if err != nil {
		return nil, err
	}
	amount, err := uint256(a.AmountCanonical)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.Grow(501)
	for _, p := range [][]byte{
		[]byte(directScope), uintBE(uint64(a.SourceDomain),4),
		uintBE(uint64(a.DestinationDomain),4),SolanaGenesisBytes[:],
		a.AssetID[:],a.RouteID[:],uintBE(a.SetID,8),uintBE(a.SourceHeight,8),
		a.SourceRegistry[:],a.Gateway[:],a.SourceRouter[:],a.SourceMailbox[:],
		a.DestinationRouter[:],fee,a.MessageID[:],a.Recipient[:],amount,
		a.DestinationISM[:],a.DestinationRegistry[:],
	} { b.Write(p) }
	if b.Len()!=501 {
		return nil, fmt.Errorf("Solana Direct-BLS preimage width %d: %w",b.Len(),codecErr)
	}
	return b.Bytes(),nil
}
func DirectSigningHash(a DirectAuthorization) ([32]byte,error) {
	encoded,err:=DirectSigningBytes(a)
	if err!=nil{return [32]byte{},err}
	return hash(encoded),nil
}

// ExactScaleAmount converts original asset atomic units to destination
// token units without rounding; SPL u64 overflow and unrepresentable dust
// fail closed. It is independent of token mint authority.
func ExactScaleAmount(amount *big.Int,sourceDecimals,targetDecimals uint8,destinationSVM bool)(*big.Int,error){
	if amount==nil||amount.Sign()<=0||amount.BitLen()>256||
		sourceDecimals>18||targetDecimals>18{return nil,codecErr}
	result:=new(big.Int).Set(amount)
	if targetDecimals>sourceDecimals {
		factor:=new(big.Int).Exp(big.NewInt(10),big.NewInt(int64(targetDecimals-sourceDecimals)),nil)
		result.Mul(result,factor)
	}else if targetDecimals<sourceDecimals{
		factor:=new(big.Int).Exp(big.NewInt(10),big.NewInt(int64(sourceDecimals-targetDecimals)),nil)
		if new(big.Int).Mod(result,factor).Sign()!=0{return nil,codecErr}
		result.Div(result,factor)
	}
	if result.Sign()<=0||result.BitLen()>256||
		(destinationSVM&&result.BitLen()>64){return nil,codecErr}
	return result,nil
}
