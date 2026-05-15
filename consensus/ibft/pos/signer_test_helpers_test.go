package pos

import (
	"sync"

	"github.com/xgr-network/xgr-node/consensus/ibft/signer"
	xcrypto "github.com/xgr-network/xgr-node/crypto"
)

var (
	testSignerOnce sync.Once
	testSignerInst signer.Signer
)

func testHeaderSigner() signer.Signer {
	testSignerOnce.Do(func() {
		key, err := xcrypto.GenerateECDSAKey()
		if err != nil {
			panic(err)
		}

		keyManager := signer.NewECDSAKeyManagerFromKey(key)
		testSignerInst = signer.NewSigner(keyManager, keyManager)
	})

	return testSignerInst
}
