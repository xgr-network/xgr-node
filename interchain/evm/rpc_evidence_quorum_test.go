package evm

import (
 "math/big"
 "testing"

 "github.com/stretchr/testify/require"
 "github.com/xgr-network/xgr-node/types"
)

func TestIndependentSourceWitnessRequiresExactBlockAndReceipt(t *testing.T){
 h:=types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
 op:=&ILNOperation{CanonicalBlockHash:h,TransactionHash:h,
  RouteID:h,MessageID:h,BlockNumber:100,DestinationDomain:8453,
  ValidatorFeeWei:big.NewInt(100),LogIndex:1}
 snap:=&ILNRouteSnapshot{Registry:types.StringToAddress("0x1111111111111111111111111111111111111111"),BlockNumber:100}
 msg:=[]byte("canonical dispatch")
 copyOp:=*op
 copySnap:=*snap
 require.True(t,sameILNWitness(op,msg,snap,&copyOp,msg,&copySnap))
 copyOp.CanonicalBlockHash=types.StringToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
 require.False(t,sameILNWitness(op,msg,snap,&copyOp,msg,&copySnap))
 copyOp=*op
 copyOp.ValidatorFeeWei=big.NewInt(101)
 require.False(t,sameILNWitness(op,msg,snap,&copyOp,msg,&copySnap))
 copyOp=*op
 require.False(t,sameILNWitness(op,msg,snap,&copyOp,[]byte("different"),&copySnap))
 copySnap.BlockNumber=99
 require.False(t,sameILNWitness(op,msg,snap,&copyOp,msg,&copySnap))
}
