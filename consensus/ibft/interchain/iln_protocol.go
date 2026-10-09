package interchain

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/big"

	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/types"
)

const (
	ILNRouteDomainV1      = "XGR_ILN_ROUTE_V2"
	ILNFeeDomainV315     = "XITA_SOURCE_FEE_V315"
	XITAAssetDomainV315 = "XITA_ASSET_V315"
	XITARouteDomainV315 = "XITA_ROUTE_V315"
	ILNCheckpointDomainV1 = "XGR_ILN_CHECKPOINT_V2"
)

// ILNRouteKey binds a deterministic asset+chain route ID to its source.
// A v3.1.5 registry forbids multiple routes for the same directed asset pair.
type ILNRouteKey struct {
	SourceChainID     uint64
	SourceDomain      uint32
	DestinationDomain uint32
	RouteID           types.Hash
}

func (k ILNRouteKey) validate() error {
	if k.SourceChainID == 0 {
		return fmt.Errorf("ILN source chain id must be non-zero")
	}
	if k.SourceDomain == 0 {
		return fmt.Errorf("ILN source domain must be non-zero")
	}
	if k.DestinationDomain == 0 {
		return fmt.Errorf("ILN destination domain must be non-zero")
	}
	if k.RouteID == types.ZeroHash {
		return fmt.Errorf("ILN route id must be non-zero")
	}
	return nil
}

func (k ILNRouteKey) MarshalBinary() ([]byte, error) {
	if err := k.validate(); err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	buf.WriteString(ILNRouteDomainV1)
	_ = binary.Write(&buf, binary.BigEndian, k.SourceChainID)
	_ = binary.Write(&buf, binary.BigEndian, k.SourceDomain)
	_ = binary.Write(&buf, binary.BigEndian, k.DestinationDomain)
	buf.Write(k.RouteID.Bytes())

	return buf.Bytes(), nil
}

func (k ILNRouteKey) Hash() (types.Hash, error) {
	raw, err := k.MarshalBinary()
	if err != nil {
		return types.ZeroHash, err
	}
	return crypto.Keccak256Hash(raw), nil
}

// XITACanonicalAssetID matches Solidity keccak256(abi.encode(
 // keccak256("XITA_ASSET_V315"), uint64(originChainID), address(originToken),
 // uint8(kind))). Native assets: kind=0, zero address; ERC20: kind=1.
func XITACanonicalAssetID(originChainID uint64, originToken types.Address, kind uint8) (types.Hash, error) {
	if originChainID == 0 || kind > 1 ||
		(kind == 0 && originToken != types.ZeroAddress) ||
		(kind == 1 && originToken == types.ZeroAddress) {
		return types.ZeroHash, fmt.Errorf("invalid canonical XITA asset identity")
	}
	raw := make([]byte, 0, 128)
	domain := crypto.Keccak256Hash([]byte(XITAAssetDomainV315))
	raw = append(raw, domain.Bytes()...)
	raw = append(raw, abiWordUint64(originChainID)...)
	token := make([]byte, 32)
	copy(token[12:], originToken.Bytes())
	raw = append(raw, token...)
	kindWord := make([]byte, 32)
	kindWord[31] = kind
	raw = append(raw, kindWord...)
	return crypto.Keccak256Hash(raw), nil
}

// XITADirectedRouteID excludes token/router/gateway addresses and fees.
// Those are immutable registered properties, not route identity inputs.
// Reverse transfers use a distinct directed route ID.
func XITADirectedRouteID(
	assetID types.Hash, sourceChainID uint64, sourceDomain uint32,
	destinationChainID uint64, destinationDomain uint32,
) (types.Hash, error) {
	if assetID == types.ZeroHash || sourceChainID == 0 || sourceDomain == 0 ||
		destinationChainID == 0 || destinationDomain == 0 ||
		sourceChainID == destinationChainID || sourceDomain == destinationDomain ||
		(sourceDomain != 1643 && destinationDomain != 1643) {
		return types.ZeroHash, fmt.Errorf("invalid XITA directed route identity")
	}
	raw := make([]byte, 0, 32*6)
	domain := crypto.Keccak256Hash([]byte(XITARouteDomainV315))
	raw = append(raw, domain.Bytes()...)
	raw = append(raw, assetID.Bytes()...)
	raw = append(raw, abiWordUint64(sourceChainID)...)
	raw = append(raw, abiWordUint32(sourceDomain)...)
	raw = append(raw, abiWordUint64(destinationChainID)...)
	raw = append(raw, abiWordUint32(destinationDomain)...)
	return crypto.Keccak256Hash(raw), nil
}

func abiWordUint64(n uint64) []byte {
	word := make([]byte, 32)
	binary.BigEndian.PutUint64(word[24:], n)
	return word
}

func abiWordUint32(n uint32) []byte {
	word := make([]byte, 32)
	binary.BigEndian.PutUint32(word[28:], n)
	return word
}

// ILNRoute contains the canonical on-chain configuration for one ILN hop.
// Enabled is registry state and is intentionally not part of the route key.
type ILNRoute struct {
	Key               ILNRouteKey
	Gateway           types.Address
	SourceRouter      types.Address
	Mailbox           types.Address
	MerkleTreeHook    types.Address
	DestinationRouter types.Address
	ValidatorFeeWei   *big.Int
	Enabled           bool
}

func (r ILNRoute) validateActive() error {
	if err := r.Key.validate(); err != nil {
		return err
	}
	if r.Gateway == types.ZeroAddress {
		return fmt.Errorf("ILN gateway must be non-zero")
	}
	if r.SourceRouter == types.ZeroAddress {
		return fmt.Errorf("ILN source router must be non-zero")
	}
	if r.Mailbox == types.ZeroAddress {
		return fmt.Errorf("ILN mailbox must be non-zero")
	}
	if r.MerkleTreeHook == types.ZeroAddress {
		return fmt.Errorf("ILN merkle tree hook must be non-zero")
	}
	if r.DestinationRouter == types.ZeroAddress {
		return fmt.Errorf("ILN destination router must be non-zero")
	}
	if err := validateILNFee(r.ValidatorFeeWei); err != nil {
		return err
	}
	if !r.Enabled {
		return fmt.Errorf("new ILN route must be enabled")
	}
	return nil
}

// ValidateILNRoute validates a canonical enabled route read from an on-chain
// ILN registry. Runtime signing must fail closed on any incomplete route.
func ValidateILNRoute(r ILNRoute) error {
	return r.validateActive()
}


// ILNSourceFeeProposal is the ONLY v3.1.5 governance message.
// One native-currency validator fee applies to every route of this source.
// Validator set, chain-wide nonce and expiry provide replay-safe BLS approval.
type ILNSourceFeeProposal struct {
	SourceChainID uint64
	SourceDomain  uint32
	Registry      types.Address
	SetID         uint64
	Nonce         uint64
	ValidUntil    uint64
	ValidatorFeeWei *big.Int
}

func (p ILNSourceFeeProposal) Validate() error {
	if p.SourceChainID == 0 || p.SourceDomain == 0 {
		return fmt.Errorf("source fee chain id and domain are required")
	}
	if p.Registry == types.ZeroAddress {
		return fmt.Errorf("source fee registry address is required")
	}
	if p.SetID == 0 || p.Nonce == 0 || p.ValidUntil == 0 {
		return fmt.Errorf("source fee validator set, nonce and expiry are required")
	}
	return validateILNFee(p.ValidatorFeeWei)
}

// Packed encoding must match XGRILNProtocol.encodeSourceFeeProposalV315:
// ASCII domain || uint64(chainId) || uint32(domain) || address(registry) ||
// uint64(setId) || uint64(nonce) || uint64(expiry) || uint256(feeWei).
func (p ILNSourceFeeProposal) MarshalBinary() ([]byte, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.WriteString(ILNFeeDomainV315)
	_ = binary.Write(&buf, binary.BigEndian, p.SourceChainID)
	_ = binary.Write(&buf, binary.BigEndian, p.SourceDomain)
	buf.Write(p.Registry.Bytes())
	_ = binary.Write(&buf, binary.BigEndian, p.SetID)
	_ = binary.Write(&buf, binary.BigEndian, p.Nonce)
	_ = binary.Write(&buf, binary.BigEndian, p.ValidUntil)
	fee, _ := marshalUint256(p.ValidatorFeeWei)
	buf.Write(fee)
	return buf.Bytes(), nil
}

func (p *ILNSourceFeeProposal) UnmarshalBinary(raw []byte) error {
	const tail = 8 + 4 + types.AddressLength + 8 + 8 + 8 + 32
	if p == nil || len(raw) != len(ILNFeeDomainV315)+tail ||
		string(raw[:len(ILNFeeDomainV315)]) != ILNFeeDomainV315 {
		return fmt.Errorf("invalid v3.1.5 source fee payload")
	}
	o := len(ILNFeeDomainV315)
	p.SourceChainID = binary.BigEndian.Uint64(raw[o:o+8]); o += 8
	p.SourceDomain = binary.BigEndian.Uint32(raw[o:o+4]); o += 4
	p.Registry = types.BytesToAddress(raw[o:o+types.AddressLength]); o += types.AddressLength
	p.SetID = binary.BigEndian.Uint64(raw[o:o+8]); o += 8
	p.Nonce = binary.BigEndian.Uint64(raw[o:o+8]); o += 8
	p.ValidUntil = binary.BigEndian.Uint64(raw[o:o+8]); o += 8
	p.ValidatorFeeWei = new(big.Int).SetBytes(raw[o:o+32])
	return p.Validate()
}

func (p ILNSourceFeeProposal) ProposalID() (types.Hash, error) {
	raw, err := p.MarshalBinary()
	if err != nil {
		return types.ZeroHash, err
	}
	return crypto.Keccak256Hash(raw), nil
}

type ILNCheckpointPayload struct {
	SourceChainID     uint64
	SourceDomain      uint32
	DestinationDomain uint32
	RouteID           types.Hash
	SetID             uint64
	SourceBlockNumber uint64
	Registry          types.Address
	Gateway           types.Address
	SourceRouter      types.Address
	Mailbox           types.Address
	MerkleTreeHook    types.Address
	DestinationRouter types.Address
	ValidatorFeeWei   *big.Int
	AuthorizedMessageID types.Hash
	Root                types.Hash
	Index               uint32
}

func (p ILNCheckpointPayload) validate() error {
	route := ILNRoute{
		Key: ILNRouteKey{
			SourceChainID:     p.SourceChainID,
			SourceDomain:      p.SourceDomain,
			DestinationDomain: p.DestinationDomain,
			RouteID:           p.RouteID,
		},
		Gateway:           p.Gateway,
		SourceRouter:      p.SourceRouter,
		Mailbox:           p.Mailbox,
		MerkleTreeHook:    p.MerkleTreeHook,
		DestinationRouter: p.DestinationRouter,
		ValidatorFeeWei:   p.ValidatorFeeWei,
		Enabled:           true,
	}
	if err := ValidateILNRoute(route); err != nil {
		return err
	}
	if p.SetID == 0 {
		return fmt.Errorf("ILN checkpoint set id must be non-zero")
	}
	if p.SourceBlockNumber == 0 {
		return fmt.Errorf("ILN checkpoint source block must be non-zero")
	}
	if p.Registry == types.ZeroAddress {
		return fmt.Errorf("ILN checkpoint registry must be non-zero")
	}
	if p.AuthorizedMessageID == types.ZeroHash {
		return fmt.Errorf("ILN checkpoint authorized message id must be non-zero")
	}
	if p.Root == types.ZeroHash {
		return fmt.Errorf("ILN checkpoint root must be non-zero")
	}
	return nil
}

func (p ILNCheckpointPayload) MarshalBinary() ([]byte, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	fee, err := marshalUint256(p.ValidatorFeeWei)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	buf.WriteString(ILNCheckpointDomainV1)
	_ = binary.Write(&buf, binary.BigEndian, p.SourceChainID)
	_ = binary.Write(&buf, binary.BigEndian, p.SourceDomain)
	_ = binary.Write(&buf, binary.BigEndian, p.DestinationDomain)
	buf.Write(p.RouteID.Bytes())
	_ = binary.Write(&buf, binary.BigEndian, p.SetID)
	_ = binary.Write(&buf, binary.BigEndian, p.SourceBlockNumber)
	buf.Write(p.Registry.Bytes())
	buf.Write(p.Gateway.Bytes())
	buf.Write(p.SourceRouter.Bytes())
	buf.Write(p.Mailbox.Bytes())
	buf.Write(p.MerkleTreeHook.Bytes())
	buf.Write(p.DestinationRouter.Bytes())
	buf.Write(fee)
	buf.Write(p.AuthorizedMessageID.Bytes())
	buf.Write(p.Root.Bytes())
	_ = binary.Write(&buf, binary.BigEndian, p.Index)

	return buf.Bytes(), nil
}

func (p *ILNCheckpointPayload) UnmarshalBinary(raw []byte) error {
	if p == nil {
		return fmt.Errorf("ILN checkpoint payload is nil")
	}
	const fixedTail = 8 + 4 + 4 + types.HashLength + 8 + 8 + types.AddressLength*6 + 32 + types.HashLength*2 + 4
	if len(raw) != len(ILNCheckpointDomainV1)+fixedTail {
		return fmt.Errorf("invalid ILN checkpoint payload length %d", len(raw))
	}
	if string(raw[:len(ILNCheckpointDomainV1)]) != ILNCheckpointDomainV1 {
		return fmt.Errorf("invalid ILN checkpoint domain")
	}

	o := len(ILNCheckpointDomainV1)
	p.SourceChainID = binary.BigEndian.Uint64(raw[o : o+8])
	o += 8
	p.SourceDomain = binary.BigEndian.Uint32(raw[o : o+4])
	o += 4
	p.DestinationDomain = binary.BigEndian.Uint32(raw[o : o+4])
	o += 4
	p.RouteID = types.BytesToHash(raw[o : o+types.HashLength])
	o += types.HashLength
	p.SetID = binary.BigEndian.Uint64(raw[o : o+8])
	o += 8
	p.SourceBlockNumber = binary.BigEndian.Uint64(raw[o : o+8])
	o += 8
	p.Registry = types.BytesToAddress(raw[o : o+types.AddressLength])
	o += types.AddressLength
	p.Gateway = types.BytesToAddress(raw[o : o+types.AddressLength])
	o += types.AddressLength
	p.SourceRouter = types.BytesToAddress(raw[o : o+types.AddressLength])
	o += types.AddressLength
	p.Mailbox = types.BytesToAddress(raw[o : o+types.AddressLength])
	o += types.AddressLength
	p.MerkleTreeHook = types.BytesToAddress(raw[o : o+types.AddressLength])
	o += types.AddressLength
	p.DestinationRouter = types.BytesToAddress(raw[o : o+types.AddressLength])
	o += types.AddressLength
	p.ValidatorFeeWei = new(big.Int).SetBytes(raw[o : o+32])
	o += 32
	p.AuthorizedMessageID = types.BytesToHash(raw[o : o+types.HashLength])
	o += types.HashLength
	p.Root = types.BytesToHash(raw[o : o+types.HashLength])
	o += types.HashLength
	p.Index = binary.BigEndian.Uint32(raw[o : o+4])

	_, err := p.MarshalBinary()
	return err
}

func (p ILNCheckpointPayload) Hash() (types.Hash, error) {
	raw, err := p.MarshalBinary()
	if err != nil {
		return types.ZeroHash, err
	}
	return crypto.Keccak256Hash(raw), nil
}

func validateILNFee(fee *big.Int) error {
	if fee == nil || fee.Sign() <= 0 {
		return fmt.Errorf("ILN validator fee must be positive")
	}
	if fee.BitLen() > 256 {
		return fmt.Errorf("ILN validator fee exceeds uint256")
	}
	return nil
}

func marshalUint256(v *big.Int) ([]byte, error) {
	out := make([]byte, 32)
	if v == nil || v.Sign() == 0 {
		return out, nil
	}
	if v.Sign() < 0 || v.BitLen() > 256 {
		return nil, fmt.Errorf("ILN uint256 value out of range")
	}
	v.FillBytes(out)
	return out, nil
}

