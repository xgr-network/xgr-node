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
	ILNGovernanceDomainV1 = "XGR_ILN_GOVERNANCE_V2"
	ILNCheckpointDomainV1 = "XGR_ILN_CHECKPOINT_V2"
)

type ILNProposalType uint8

const (
	ILNProposalFeeUpdate    ILNProposalType = 1
	ILNProposalRouteAdd     ILNProposalType = 2
	ILNProposalRouteEnable  ILNProposalType = 3
	ILNProposalRouteDisable ILNProposalType = 4
)

// ILNRouteKey identifies one canonical ILN hop. RouteID permits multiple
// independent routes between the same source and destination domains without
// assigning asset semantics to the node protocol.
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

func (r ILNRoute) validateForAdd() error {
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
	return r.validateForAdd()
}


// ILNGovernanceProposal is the canonical payload signed by Interchain
// validators for ILN protocol governance.
//
// SetID binds approval to a specific validator-set version.
// Nonce provides replay protection at the route registry.
// ValidUntil bounds the lifetime of an unexecuted proposal.
type ILNGovernanceProposal struct {
	Type       ILNProposalType
	Registry   types.Address
	SetID      uint64
	Nonce      uint64
	ValidUntil uint64
	Route      ILNRoute
}

func (p ILNGovernanceProposal) validate() error {
	if err := p.Route.Key.validate(); err != nil {
		return err
	}
	if p.Registry == types.ZeroAddress {
		return fmt.Errorf("ILN governance registry must be non-zero")
	}
	if p.SetID == 0 {
		return fmt.Errorf("ILN governance set id must be non-zero")
	}
	if p.Nonce == 0 {
		return fmt.Errorf("ILN governance nonce must be non-zero")
	}
	if p.ValidUntil == 0 {
		return fmt.Errorf("ILN governance valid-until must be non-zero")
	}

	switch p.Type {
	case ILNProposalFeeUpdate:
		if !emptyILNRouteContracts(p.Route) || p.Route.Enabled {
			return fmt.Errorf("ILN fee update must contain only route key and fee")
		}
		return validateILNFee(p.Route.ValidatorFeeWei)
	case ILNProposalRouteAdd:
		return p.Route.validateForAdd()
	case ILNProposalRouteEnable, ILNProposalRouteDisable:
		if !emptyILNRouteContracts(p.Route) || p.Route.Enabled || nonZeroBigInt(p.Route.ValidatorFeeWei) {
			return fmt.Errorf("ILN route state proposal must contain only route key")
		}
		return nil
	default:
		return fmt.Errorf("unsupported ILN proposal type %d", p.Type)
	}
}

func (p ILNGovernanceProposal) MarshalBinary() ([]byte, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}

	fee, err := marshalUint256(p.Route.ValidatorFeeWei)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	buf.WriteString(ILNGovernanceDomainV1)
	_ = binary.Write(&buf, binary.BigEndian, p.Route.Key.SourceChainID)
	_ = binary.Write(&buf, binary.BigEndian, p.Route.Key.SourceDomain)
	_ = binary.Write(&buf, binary.BigEndian, p.Route.Key.DestinationDomain)
	buf.Write(p.Route.Key.RouteID.Bytes())
	buf.Write(p.Registry.Bytes())
	_ = binary.Write(&buf, binary.BigEndian, p.SetID)
	_ = binary.Write(&buf, binary.BigEndian, p.Nonce)
	_ = binary.Write(&buf, binary.BigEndian, p.ValidUntil)
	buf.WriteByte(byte(p.Type))
	buf.Write(p.Route.Gateway.Bytes())
	buf.Write(p.Route.SourceRouter.Bytes())
	buf.Write(p.Route.Mailbox.Bytes())
	buf.Write(p.Route.MerkleTreeHook.Bytes())
	buf.Write(p.Route.DestinationRouter.Bytes())
	buf.Write(fee)

	return buf.Bytes(), nil
}

func (p *ILNGovernanceProposal) UnmarshalBinary(raw []byte) error {
	if p == nil {
		return fmt.Errorf("ILN governance proposal is nil")
	}

	const fixedTail = 8 + 4 + 4 + types.HashLength + types.AddressLength + 8 + 8 + 8 + 1 +
		types.AddressLength*5 + 32
	if len(raw) != len(ILNGovernanceDomainV1)+fixedTail {
		return fmt.Errorf("invalid ILN governance payload length %d", len(raw))
	}
	if string(raw[:len(ILNGovernanceDomainV1)]) != ILNGovernanceDomainV1 {
		return fmt.Errorf("invalid ILN governance domain")
	}

	o := len(ILNGovernanceDomainV1)
	p.Route.Key.SourceChainID = binary.BigEndian.Uint64(raw[o : o+8])
	o += 8
	p.Route.Key.SourceDomain = binary.BigEndian.Uint32(raw[o : o+4])
	o += 4
	p.Route.Key.DestinationDomain = binary.BigEndian.Uint32(raw[o : o+4])
	o += 4
	p.Route.Key.RouteID = types.BytesToHash(raw[o : o+types.HashLength])
	o += types.HashLength
	p.Registry = types.BytesToAddress(raw[o : o+types.AddressLength])
	o += types.AddressLength
	p.SetID = binary.BigEndian.Uint64(raw[o : o+8])
	o += 8
	p.Nonce = binary.BigEndian.Uint64(raw[o : o+8])
	o += 8
	p.ValidUntil = binary.BigEndian.Uint64(raw[o : o+8])
	o += 8
	p.Type = ILNProposalType(raw[o])
	o++
	p.Route.Gateway = types.BytesToAddress(raw[o : o+types.AddressLength])
	o += types.AddressLength
	p.Route.SourceRouter = types.BytesToAddress(raw[o : o+types.AddressLength])
	o += types.AddressLength
	p.Route.Mailbox = types.BytesToAddress(raw[o : o+types.AddressLength])
	o += types.AddressLength
	p.Route.MerkleTreeHook = types.BytesToAddress(raw[o : o+types.AddressLength])
	o += types.AddressLength
	p.Route.DestinationRouter = types.BytesToAddress(raw[o : o+types.AddressLength])
	o += types.AddressLength
	p.Route.ValidatorFeeWei = new(big.Int).SetBytes(raw[o : o+32])

	// Enabled is registry state. Proposal encoding uses it only as a validation
	// marker for ROUTE_ADD, where the canonical route is introduced enabled.
	p.Route.Enabled = p.Type == ILNProposalRouteAdd

	_, err := p.MarshalBinary()
	return err
}

func (p ILNGovernanceProposal) Hash() (types.Hash, error) {
	raw, err := p.MarshalBinary()
	if err != nil {
		return types.ZeroHash, err
	}
	return crypto.Keccak256Hash(raw), nil
}

// ProposalID is the canonical replay-safe content identifier presented by the
// CLI and used by the worker when collecting governance votes.
func (p ILNGovernanceProposal) ProposalID() (types.Hash, error) {
	return p.Hash()
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

func emptyILNRouteContracts(r ILNRoute) bool {
	return r.Gateway == types.ZeroAddress &&
		r.SourceRouter == types.ZeroAddress &&
		r.Mailbox == types.ZeroAddress &&
		r.MerkleTreeHook == types.ZeroAddress &&
		r.DestinationRouter == types.ZeroAddress
}

func nonZeroBigInt(v *big.Int) bool {
	return v != nil && v.Sign() != 0
}
