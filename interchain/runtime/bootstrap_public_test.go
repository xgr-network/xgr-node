package runtime

import (
 "bytes"
 "encoding/hex"
 "math/big"
 "os"
 "strings"
 "testing"

 "github.com/stretchr/testify/require"
 stakingcontract "github.com/xgr-network/xgr-node/contracts/staking"
 "github.com/xgr-network/xgr-node/crypto"
 "github.com/xgr-network/xgr-node/types"
)

func TestBootstrapDomainsAreExplicitBoundedAndUnique(t *testing.T){
 domains,err:=parseILNBootstrapDomains("42161,8453")
 require.NoError(t,err)
 require.Equal(t,[]uint32{8453,42161},domains)
 empty,err:=parseILNBootstrapDomains("")
 require.NoError(t,err);require.Empty(t,empty)
 for _,v:=range []string{"0","x","42161,42161","42161,","4294967296",strings.Repeat("1,",17)}{
  _,err:=parseILNBootstrapDomains(v)
  require.Error(t,err,v)
 }
}

func testBootstrapPublicWorker(t *testing.T)(*Worker,types.Address){
 t.Helper()
 key,err:=crypto.GenerateBLSKey()
 require.NoError(t,err)
 raw,err:=key.MarshalBinary()
 require.NoError(t,err)
 pub,err:=crypto.BLSSecretKeyToPubkeyBytes(key)
 require.NoError(t,err)
 address:=types.StringToAddress("0x98F8bC086454B8386788244eee9A43d5D0b4E63E")
 w:=&Worker{
  dataDir:t.TempDir(),
  blsRaw:[]byte(hex.EncodeToString(raw)),blsPubKey:pub,
  localAddr:address,bootstrapDomains:[]uint32{42161},
  state:&fakeInterchainState{origin:1643,validators:map[types.Address]*stakingcontract.ValidatorInfo{
    address:{Exists:true,Active:true,StakedAmount:big.NewInt(1),
     BLSPubKey:bytes.Clone(pub)},
  }},
 }
 return w,address
}

func TestBootstrapProofP2POnlyAcceptsActiveMatchingPoSKeyAndSignedDomain(t *testing.T){
 w,address:=testBootstrapPublicWorker(t)
 p,err:=w.makeBootstrapPublicProof(42161)
 require.NoError(t,err)
 require.Equal(t,uint32(42161),p.DestinationDomain)
 require.Equal(t,address.String(),p.Validator)
 require.NoError(t,w.acceptBootstrapPublicProof(p))
 stored,err:=os.ReadFile(w.bootstrapPublicProofPath(42161,address))
 require.NoError(t,err)
 require.Contains(t,string(stored),"\"possessionProof\"")
 // No peer can swap the chain, identity, canonical payload, key or signature.
 wrongDomain:=p;wrongDomain.DestinationDomain=8453
 require.Error(t,w.acceptBootstrapPublicProof(wrongDomain))
 wrongPayload:=p;wrongPayload.Payload="0x00"
 require.Error(t,w.acceptBootstrapPublicProof(wrongPayload))
 wrongSignature:=p;wrongSignature.CompressedProof="0x"+strings.Repeat("0",192)
 require.Error(t,w.acceptBootstrapPublicProof(wrongSignature))
 wrongEIP:=p;wrongEIP.PossessionProof="0x"+strings.Repeat("0",512)
 require.Error(t,w.acceptBootstrapPublicProof(wrongEIP))
 status:=w.state.(*fakeInterchainState).validators[address]
 status.BLSPubKey=bytes.Repeat([]byte{0x44},48)
 require.Error(t,w.acceptBootstrapPublicProof(p))
 require.Error(t,func()error{_,e:=w.makeBootstrapPublicProof(42161);return e}())
 status.BLSPubKey=bytes.Clone(w.blsPubKey)
 status.Active=false
 require.Error(t,w.acceptBootstrapPublicProof(p))
 status.Active=true
 require.Error(t,func()error{_,e:=w.makeBootstrapPublicProof(8453);return e}())
}
