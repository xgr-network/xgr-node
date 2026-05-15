package e2e

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/umbracle/ethgo"
	"github.com/umbracle/ethgo/abi"
	"github.com/xgr-network/xgr-node/e2e/framework"
	"github.com/xgr-network/xgr-node/helper/hex"
	"github.com/xgr-network/xgr-node/types"
)

const stakingV2ReentrantDelegatorBytecode = `608060405234801561001057600080fd5b50610812806100206000396000f3fe6080604052600436106100955760003560e01c80635c19a95c116100595780635c19a95c146101895780638c9e2cdb1461019c578063917f51b4146101f6578063953c368c14610210578063aa8c217c14610230576100a4565b806301a5d67e146100ac578063295a5212146100cc5780632f830600146100fd5780633a5381b51461011d57806359703b2e1461015a576100a4565b366100a4576100a2610254565b005b6100a2610254565b3480156100b857600080fd5b506100a26100c73660046106e1565b610499565b3480156100d857600080fd5b506000546100e69060ff1681565b60405160ff90911681526020015b60405180910390f35b34801561010957600080fd5b506100a261011836600461070b565b610558565b34801561012957600080fd5b506000546101429061010090046001600160a01b031681565b6040516001600160a01b0390911681526020016100f4565b34801561016657600080fd5b5060025461017990610100900460ff1681565b60405190151581526020016100f4565b6100a2610197366004610747565b610589565b3480156101a857600080fd5b506100a26101b7366004610769565b600080546001600160a01b03909316610100026001600160a81b031990931660ff90941693909317919091179091556001556002805461ffff19169055565b34801561020257600080fd5b506002546101799060ff1681565b34801561021c57600080fd5b506100a261022b366004610747565b610633565b34801561023c57600080fd5b5061024660015481565b6040519081526020016100f4565b60025460ff1680610268575060005460ff16155b1561026f57565b6002805460ff1916600190811790915560008054909160ff91909116900361034e576000546001546040516101009092046001600160a01b0316602483015260448201526110019062d2eb3f60e11b906064015b60408051601f198184030181529181526020820180516001600160e01b03166001600160e01b031990941693909317909252905161030191906107ad565b6000604051808303816000865af19150503d806000811461033e576040519150601f19603f3d011682016040523d82523d6000602084013e610343565b606091505b50508091505061047f565b60005460ff1660020361038a576000546040516101009091046001600160a01b031660248201526110019063254f0da360e21b906044016102c3565b60005460ff166003036103cc576000546040516101009091046001600160a01b0316602482015260016044820152611001906217c18360e91b906064016102c3565b60005460ff1660040361047f57600154600054604080516101009092046001600160a01b031660248084019190915281518084039091018152604490920181526020820180516317066a5760e21b6001600160e01b03909116179052516110019291610437916107ad565b60006040518083038185875af1925050503d8060008114610474576040519150601f19603f3d011682016040523d82523d6000602084013e610479565b606091505b50909150505b600280549115156101000261ff0019909216919091179055565b6040516001600160a01b0383166024820152604481018290526000906110019062d2eb3f60e11b906064015b60408051601f198184030181529181526020820180516001600160e01b03166001600160e01b031990941693909317909252905161050391906107ad565b6000604051808303816000865af19150503d8060008114610540576040519150601f19603f3d011682016040523d82523d6000602084013e610545565b606091505b505090508061055357600080fd5b505050565b6040516001600160a01b03831660248201528115156044820152600090611001906217c18360e91b906064016104c5565b604080516001600160a01b03831660248083019190915282518083039091018152604490910182526020810180516001600160e01b03166317066a5760e21b17905290516000916110019134916105df916107ad565b60006040518083038185875af1925050503d806000811461061c576040519150601f19603f3d011682016040523d82523d6000602084013e610621565b606091505b505090508061062f57600080fd5b5050565b604080516001600160a01b03831660248083019190915282518083039091018152604490910182526020810180516001600160e01b031663254f0da360e21b17905290516000916110019161068891906107ad565b6000604051808303816000865af19150503d806000811461061c576040519150601f19603f3d011682016040523d82523d6000602084013e610621565b80356001600160a01b03811681146106dc57600080fd5b919050565b600080604083850312156106f457600080fd5b6106fd836106c5565b946020939093013593505050565b6000806040838503121561071e57600080fd5b610727836106c5565b91506020830135801515811461073c57600080fd5b809150509250929050565b60006020828403121561075957600080fd5b610762826106c5565b9392505050565b60008060006060848603121561077e57600080fd5b833560ff8116811461078f57600080fd5b925061079d602085016106c5565b9150604084013590509250925092565b6000825160005b818110156107ce57602081860181015185830152016107b4565b50600092019182525091905056fea2646970667358221220b8e609baa2397c762c86851e83c219ebf23fdf822fa0bd2874b287a82fc4b09564736f6c63430008130033`

const stakingV2ReentrantDelegatorABI = `[{"stateMutability":"payable","type":"fallback"},{"inputs":[],"name":"amount","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"attempted","outputs":[{"internalType":"bool","name":"","type":"bool"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"uint8","name":"mode_","type":"uint8"},{"internalType":"address","name":"validator_","type":"address"},{"internalType":"uint256","name":"amount_","type":"uint256"}],"name":"configure","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"address","name":"validator_","type":"address"}],"name":"delegate","outputs":[],"stateMutability":"payable","type":"function"},{"inputs":[],"name":"mode","outputs":[{"internalType":"uint8","name":"","type":"uint8"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"reentrySucceeded","outputs":[{"internalType":"bool","name":"","type":"bool"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"validator_","type":"address"},{"internalType":"bool","name":"active","type":"bool"}],"name":"setDelegationActive","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"address","name":"validator_","type":"address"}],"name":"unstakeDelegation","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[],"name":"validator","outputs":[{"internalType":"address","name":"","type":"address"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"validator_","type":"address"},{"internalType":"uint256","name":"amount_","type":"uint256"}],"name":"withdrawDelegation","outputs":[],"stateMutability":"nonpayable","type":"function"},{"stateMutability":"payable","type":"receive"}]`

var stakingV2ReentrantDelegatorParsedABI = abi.MustNewABI(stakingV2ReentrantDelegatorABI)

const (
	stakingV2ReentrantDelegatorGasLimit uint64 = framework.DefaultGasLimit

	stakingV2ReentryModeWithdraw uint8 = 1
	stakingV2ReentryModeUnstake  uint8 = 2
)

func deployStakingV2ReentrantDelegator(t *testing.T, srv *framework.TestServer, from types.Address, key *ecdsa.PrivateKey) types.Address {
	t.Helper()

	bytecode, err := hex.DecodeString(stakingV2ReentrantDelegatorBytecode)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), framework.DefaultTimeout)
	defer cancel()

	receipt, err := srv.SendRawTx(ctx, &framework.PreparedTransaction{
		From:     from,
		GasPrice: framework.TestGasPrice(),
		Gas:      stakingV2ReentrantDelegatorGasLimit,
		Value:    big.NewInt(0),
		Input:    bytecode,
	}, key)
	require.NoError(t, err)
	require.NotNil(t, receipt)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)
	require.NotEqual(t, ethgo.ZeroAddress, receipt.ContractAddress)

	return types.Address(receipt.ContractAddress)
}

func sendStakingV2ReentrantDelegatorTx(
	srv *framework.TestServer,
	from types.Address,
	key *ecdsa.PrivateKey,
	contract types.Address,
	method string,
	value *big.Int,
	args map[string]interface{},
) (*ethgo.Receipt, error) {
	m := stakingV2ReentrantDelegatorParsedABI.Methods[method]
	if m == nil {
		return nil, errors.New("reentrant delegator ABI missing " + method)
	}
	inp, err := m.Inputs.Encode(args)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), framework.DefaultTimeout)
	defer cancel()

	return srv.SendRawTx(ctx, &framework.PreparedTransaction{
		From:     from,
		To:       &contract,
		GasPrice: framework.TestGasPrice(),
		Gas:      stakingV2ReentrantDelegatorGasLimit,
		Value:    value,
		Input:    append(m.ID(), inp...),
	}, key)
}

func queryStakingV2ReentrantDelegatorBool(srv *framework.TestServer, from, contract types.Address, method string) (bool, error) {
	m := stakingV2ReentrantDelegatorParsedABI.Methods[method]
	if m == nil {
		return false, errors.New("reentrant delegator ABI missing " + method)
	}
	to := ethgo.Address(contract)
	resp, err := srv.JSONRPC().Eth().Call(&ethgo.CallMsg{
		From:     ethgo.Address(from),
		To:       &to,
		Data:     m.ID(),
		GasPrice: framework.TestGasPriceUint64(),
		Value:    big.NewInt(0),
	}, ethgo.Latest)
	if err != nil {
		return false, err
	}
	b, err := hex.DecodeHex(resp)
	if err != nil {
		return false, err
	}
	decoded, err := m.Outputs.Decode(b)
	if err != nil {
		return false, err
	}
	root, ok := decoded.(map[string]interface{})
	if !ok {
		return false, errors.New("reentrant delegator bool decode root")
	}
	value, ok := root["0"].(bool)
	if !ok {
		return false, errors.New("reentrant delegator bool decode value")
	}

	return value, nil
}
