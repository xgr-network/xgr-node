package types

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/umbracle/fastrlp"
)

func TestAccessListTx_RLPRoundtrip_WithAccessList(t *testing.T) {
	to := StringToAddress("11")
	from := StringToAddress("22")

	var k1, k2 Hash
	k1[31] = 0x01
	k2[31] = 0x02

	al := AccessList{
		{
			Address:     StringToAddress("33"),
			StorageKeys: []Hash{k1, k2},
		},
		{
			Address:     StringToAddress("44"),
			StorageKeys: []Hash{},
		},
	}

	tx := &Transaction{
		Type:       AccessListTx,
		ChainID:    big.NewInt(1),
		Nonce:      1,
		GasPrice:   big.NewInt(7),
		Gas:        21000,
		To:         &to,
		From:       from,
		Value:      big.NewInt(0),
		Input:      []byte{0xAA, 0xBB},
		AccessList: al,
		V:          big.NewInt(1),
		R:          big.NewInt(2),
		S:          big.NewInt(3),
	}

	raw := tx.MarshalRLP()

	var got Transaction
	require.NoError(t, got.UnmarshalRLP(raw))
	require.Equal(t, AccessListTx, got.Type)
	require.NotNil(t, got.ChainID)
	require.Equal(t, int64(1), got.ChainID.Int64())
	require.Equal(t, al, got.AccessList)
}

func TestAccessListTx_RLP_InvalidAccessTupleShape(t *testing.T) {
	ar := &fastrlp.Arena{}

	// payload: [chainId, nonce, gasPrice, gas, to, value, input, accessList, v, r, s]
	payload := ar.NewArray()
	payload.Set(ar.NewUint(1))               // chainId
	payload.Set(ar.NewUint(0))               // nonce
	payload.Set(ar.NewBigInt(big.NewInt(1))) // gasPrice
	payload.Set(ar.NewUint(21000))           // gas
	payload.Set(ar.NewNull())                // to (null ok)
	payload.Set(ar.NewBigInt(big.NewInt(0))) // value
	payload.Set(ar.NewCopyBytes([]byte{}))   // input

	// accessList with invalid tuple length (should be 2 elems, we give 1)
	accessList := ar.NewArray()
	tuple := ar.NewArray()
	tuple.Set(ar.NewCopyBytes(make([]byte, 20))) // address only, missing keys array
	accessList.Set(tuple)
	payload.Set(accessList)

	payload.Set(ar.NewUint(1))               // v
	payload.Set(ar.NewBigInt(big.NewInt(1))) // r
	payload.Set(ar.NewBigInt(big.NewInt(1))) // s

	raw := append([]byte{byte(AccessListTx)}, payload.MarshalTo(nil)...)

	var tx Transaction
	err := tx.UnmarshalRLP(raw)
	require.Error(t, err)
}
