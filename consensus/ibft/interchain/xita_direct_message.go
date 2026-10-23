package interchain

import (
    "bytes"
    "encoding/binary"
    "fmt"
    "math/big"

    "github.com/xgr-network/xgr-node/crypto"
    "github.com/xgr-network/xgr-node/types"
)

// XITADirectMessageDomain is the only transport BLS domain after the hard
// reset. It must exactly match XITADirectProtocol.sol.
const XITADirectMessageDomain = "XITA_DIRECT_MESSAGE_V1"

// XITADirectMessage binds one fee-qualified Hyperlane Message ID to one
// canonical source, route, destination, and current destination validator set.
// No Merkle root, proof, leaf index or checkpoint is involved.
type XITADirectMessage struct {
    SourceChainID uint64
    SourceDomain uint32
    DestinationDomain uint32
    RouteID types.Hash
    SetID uint64
    SourceBlockNumber uint64
    SourceRegistry types.Address
    Gateway types.Address
    SourceRouter types.Address
    SourceMailbox types.Address
    DestinationRouter types.Address
    ValidatorFeeWei *big.Int
    MessageID types.Hash
    DestinationISM types.Address
    DestinationRegistry types.Address
}

func (a XITADirectMessage) MarshalBinary() ([]byte,error) {
    if a.SourceChainID==0 || a.SourceDomain==0 || a.DestinationDomain==0 ||
        a.SourceDomain==a.DestinationDomain || a.RouteID==types.ZeroHash ||
        a.SetID==0 || a.SourceBlockNumber==0 || a.SourceRegistry==types.ZeroAddress ||
        a.Gateway==types.ZeroAddress || a.SourceRouter==types.ZeroAddress ||
        a.SourceMailbox==types.ZeroAddress || a.DestinationRouter==types.ZeroAddress ||
        a.ValidatorFeeWei==nil || a.ValidatorFeeWei.Sign()<=0 ||
        a.ValidatorFeeWei.BitLen()>256 || a.MessageID==types.ZeroHash ||
        a.DestinationISM==types.ZeroAddress ||
        a.DestinationRegistry==types.ZeroAddress {
        return nil,fmt.Errorf("invalid direct BLS message authorization")
    }
    var out bytes.Buffer
    out.WriteString(XITADirectMessageDomain)
    _=binary.Write(&out,binary.BigEndian,a.SourceChainID)
    _=binary.Write(&out,binary.BigEndian,a.SourceDomain)
    _=binary.Write(&out,binary.BigEndian,a.DestinationDomain)
    out.Write(a.RouteID.Bytes())
    _=binary.Write(&out,binary.BigEndian,a.SetID)
    _=binary.Write(&out,binary.BigEndian,a.SourceBlockNumber)
    out.Write(a.SourceRegistry.Bytes())
    out.Write(a.Gateway.Bytes())
    out.Write(a.SourceRouter.Bytes())
    out.Write(a.SourceMailbox.Bytes())
    out.Write(a.DestinationRouter.Bytes())
    fee:=make([]byte,32)
    a.ValidatorFeeWei.FillBytes(fee)
    out.Write(fee)
    out.Write(a.MessageID.Bytes())
    out.Write(a.DestinationISM.Bytes())
    out.Write(a.DestinationRegistry.Bytes())
    return out.Bytes(),nil
}

// UnmarshalBinary must be unambiguous: reject any extra, omitted or
// noncanonical data before an incoming peer vote is considered.
func (a *XITADirectMessage) UnmarshalBinary(raw []byte) error {
    const size=8+4+4+32+8+8+5*20+32+32+20+20
    if a==nil || len(raw)!=len(XITADirectMessageDomain)+size ||
        string(raw[:len(XITADirectMessageDomain)])!=XITADirectMessageDomain {
        return fmt.Errorf("invalid direct BLS payload length or domain")
    }
    o:=len(XITADirectMessageDomain)
    a.SourceChainID=binary.BigEndian.Uint64(raw[o:o+8]);o+=8
    a.SourceDomain=binary.BigEndian.Uint32(raw[o:o+4]);o+=4
    a.DestinationDomain=binary.BigEndian.Uint32(raw[o:o+4]);o+=4
    a.RouteID=types.BytesToHash(raw[o:o+32]);o+=32
    a.SetID=binary.BigEndian.Uint64(raw[o:o+8]);o+=8
    a.SourceBlockNumber=binary.BigEndian.Uint64(raw[o:o+8]);o+=8
    a.SourceRegistry=types.BytesToAddress(raw[o:o+20]);o+=20
    a.Gateway=types.BytesToAddress(raw[o:o+20]);o+=20
    a.SourceRouter=types.BytesToAddress(raw[o:o+20]);o+=20
    a.SourceMailbox=types.BytesToAddress(raw[o:o+20]);o+=20
    a.DestinationRouter=types.BytesToAddress(raw[o:o+20]);o+=20
    a.ValidatorFeeWei=new(big.Int).SetBytes(raw[o:o+32]);o+=32
    a.MessageID=types.BytesToHash(raw[o:o+32]);o+=32
    a.DestinationISM=types.BytesToAddress(raw[o:o+20]);o+=20
    a.DestinationRegistry=types.BytesToAddress(raw[o:o+20])
    _,err:=a.MarshalBinary()
    return err
}

// UnmarshalBinary decodes exactly the Solidity XITADirectProtocol V1 format.
// A P2P peer's arbitrary payload never overrides independently verified
// source-transaction and destination-validator-set evidence.
func (a *XITADirectMessage) UnmarshalBinary(raw []byte) error {
    const tail = 8+4+4+32+8+8+5*types.AddressLength+32+types.HashLength+2*types.AddressLength
    if a==nil || len(raw)!=len(XITADirectMessageDomain)+tail ||
        string(raw[:len(XITADirectMessageDomain)])!=XITADirectMessageDomain {
        return fmt.Errorf("invalid XITA direct message signing payload")
    }
    at:=len(XITADirectMessageDomain)
    a.SourceChainID=binary.BigEndian.Uint64(raw[at:at+8]);at+=8
    a.SourceDomain=binary.BigEndian.Uint32(raw[at:at+4]);at+=4
    a.DestinationDomain=binary.BigEndian.Uint32(raw[at:at+4]);at+=4
    a.RouteID=types.BytesToHash(raw[at:at+types.HashLength]);at+=types.HashLength
    a.SetID=binary.BigEndian.Uint64(raw[at:at+8]);at+=8
    a.SourceBlockNumber=binary.BigEndian.Uint64(raw[at:at+8]);at+=8
    a.SourceRegistry=types.BytesToAddress(raw[at:at+types.AddressLength]);at+=types.AddressLength
    a.Gateway=types.BytesToAddress(raw[at:at+types.AddressLength]);at+=types.AddressLength
    a.SourceRouter=types.BytesToAddress(raw[at:at+types.AddressLength]);at+=types.AddressLength
    a.SourceMailbox=types.BytesToAddress(raw[at:at+types.AddressLength]);at+=types.AddressLength
    a.DestinationRouter=types.BytesToAddress(raw[at:at+types.AddressLength]);at+=types.AddressLength
    a.ValidatorFeeWei=new(big.Int).SetBytes(raw[at:at+32]);at+=32
    a.MessageID=types.BytesToHash(raw[at:at+types.HashLength]);at+=types.HashLength
    a.DestinationISM=types.BytesToAddress(raw[at:at+types.AddressLength]);at+=types.AddressLength
    a.DestinationRegistry=types.BytesToAddress(raw[at:at+types.AddressLength])
    _,err:=a.MarshalBinary()
    return err
}

func (a XITADirectMessage) Hash() (types.Hash,error) {
    b,err:=a.MarshalBinary()
    if err!=nil{return types.ZeroHash,err}
    return crypto.Keccak256Hash(b),nil
}
