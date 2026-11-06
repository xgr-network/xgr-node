package solana

import (
	"bytes"
	"encoding/hex"
	"math/big"
	"strings"
	"testing"
)

func mustHex(t *testing.T,s string) [32]byte {
	t.Helper()
	b,err:=hex.DecodeString(strings.TrimPrefix(s,"0x"))
	if err!=nil||len(b)!=32{t.Fatalf("invalid test hex %q: %v",s,err)}
	var out [32]byte;copy(out[:],b);return out
}
func TestSolanaBase58Vectors(t *testing.T){
	for _,q:=range []struct{key,hex string}{
		{"5eykt4UsFv8P8NJdTREpY1vzqKqZKvdpKuc147dw2N9d","45296998a6f8e2a784db5d9f95e18fc23f70441a1039446801089879b08c7ef0"},
		{"So11111111111111111111111111111111111111112","069b8857feab8184fb687f634618c035dac439dc1aeb3b5598a0f00000000001"},
		{"TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA","06ddf6e1d765a193d9cbe146ceeb79ac1cb485ed5f5b37913a8cf5857eff00a9"},
	}{
		got,err:=DecodeSolanaPubkey(q.key)
		if err!=nil||got!=mustHex(t,q.hex){t.Fatalf("base58 %s: %x %v",q.key,got,err)}
	}
	for _,bad:=range []string{strings.Repeat("O",44),strings.Repeat("1",31)}{
		if _,err:=DecodeSolanaPubkey(bad);err==nil{t.Fatal("invalid base58 accepted")}
	}
}
func fixture(t *testing.T) DirectAuthorization {
	t.Helper()
	p:=func(s string)[32]byte{return mustHex(t,s)}
	return DirectAuthorization{
		SourceDomain:1643,DestinationDomain:1399811149,
		AssetID:p("a7585b8fbd074c1a7fa6df8b8e2e5ce73395ccbb64bf99b631c5da8ff2ac44e0"),
		RouteID:p("185ea4af59c1c6160ca69ab81f0621fe4d823eab02cfd5c5e9decbd6000dadf6"),
		SetID:7,SourceHeight:12345678,
		SourceRegistry:p(strings.Repeat("0",60)+"b001"),
		Gateway:p(strings.Repeat("0",60)+"b002"),
		SourceRouter:p(strings.Repeat("0",60)+"a11c"),
		SourceMailbox:p(strings.Repeat("0",60)+"b003"),
		DestinationRouter:p("06ddf6e1d765a193d9cbe146ceeb79ac1cb485ed5f5b37913a8cf5857eff00a9"),
		ValidatorFeeAtomic:big.NewInt(123456789),MessageID:p(strings.Repeat("11",32)),
		Recipient:p(strings.Repeat("33",32)),
		AmountCanonical:big.NewInt(1000000000000000000),
		DestinationISM:p("069b8857feab8184fb687f634618c035dac439dc1aeb3b5598a0f00000000001"),
		DestinationRegistry:p(strings.Repeat("22",32)),
	}
}
func TestAssetAndRouteGoldenVectors(t *testing.T){
	a:=fixture(t)
	mint:=mustHex(t,"069b8857feab8184fb687f634618c035dac439dc1aeb3b5598a0f00000000001")
	asset,err:=SolanaAssetID(mint,1)
	if err!=nil||asset!=mustHex(t,"d2aefcda8943cf2e0983e100de0325d0d1de575deb4f476e562c0e120db3957d"){t.Fatalf("asset: %x %v",asset,err)}
	got,err:=SolanaRouteID(a.AssetID,a.SourceDomain,a.DestinationDomain,a.SourceRouter,a.DestinationRouter)
	if err!=nil||got!=a.RouteID{t.Fatalf("outbound route: %x %v",got,err)}
	reverse,err:=SolanaRouteID(a.AssetID,a.DestinationDomain,a.SourceDomain,a.DestinationRouter,a.SourceRouter)
	if err!=nil||reverse!=mustHex(t,"f53653c35f340c4903ad09747d10d816aa59194f841822ae24114e5df6f3b263"){t.Fatalf("reverse route: %x %v",reverse,err)}
	if _,err:=SolanaRouteID(a.AssetID,8453,1399811149,a.SourceRouter,a.DestinationRouter);err==nil{t.Fatal("spoke-to-spoke accepted")}
	if _,err:=SolanaRouteID(a.AssetID,1643,1399811149,a.DestinationRouter,a.SourceRouter);err==nil{t.Fatal("EVM address spoof accepted")}
	if _,err:=SolanaAssetID(mint,0);err==nil{t.Fatal("malformed SPL/native kind")}
}
func TestDirectSigningGoldenHash(t *testing.T){
	a:=fixture(t)
	encoded,err:=DirectSigningBytes(a)
	if err!=nil||len(encoded)!=501{t.Fatalf("bytes: %d %v",len(encoded),err)}
	got,err:=DirectSigningHash(a)
	if err!=nil||got!=mustHex(t,"378e45af3059a027847165ac3e2bccd9fbc076d425f06fb3198c126ab2cfaa10"){t.Fatalf("golden hash: %x %v",got,err)}
	a.SetID++
	changed,err:=DirectSigningHash(a)
	if err!=nil||bytes.Equal(got[:],changed[:]){t.Fatal("set ID not bound")}
	a=fixture(t);a.RouteID=mustHex(t,"f53653c35f340c4903ad09747d10d816aa59194f841822ae24114e5df6f3b263")
	if _,err:=DirectSigningBytes(a);err==nil{t.Fatal("wrong route accepted")}
	a=fixture(t);a.SourceMailbox=a.DestinationRouter
	if _,err:=DirectSigningBytes(a);err==nil{t.Fatal("foreign mailbox accepted")}
}
func TestExactScaleAmountNoDustOrOverflow(t *testing.T){
	one:=big.NewInt(1000000000000000000)
	out,err:=ExactScaleAmount(one,18,9,true)
	if err!=nil||out.Cmp(big.NewInt(1000000000))!=0{t.Fatalf("xgr->spl %s %v",out,err)}
	back,err:=ExactScaleAmount(out,9,18,false)
	if err!=nil||back.Cmp(one)!=0{t.Fatalf("spl->xgr %s %v",back,err)}
	if _,err:=ExactScaleAmount(big.NewInt(1),18,9,true);err==nil{t.Fatal("dust rounded")}
	limit:=new(big.Int).Lsh(big.NewInt(1),64);limit.Mul(limit,big.NewInt(1000000000))
	if _,err:=ExactScaleAmount(limit,18,9,true);err==nil{t.Fatal("u64 overflow")}
}
