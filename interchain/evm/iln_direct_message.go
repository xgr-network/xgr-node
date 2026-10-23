package evm

import (
    "bytes"
    "encoding/binary"
    "fmt"

    "github.com/xgr-network/xgr-node/crypto"
    "github.com/xgr-network/xgr-node/types"
)

// MaxILNSerializedMessageBytes limits one Hyperlane V3 message decoded
// directly from the successful source Mailbox Dispatch event.
const MaxILNSerializedMessageBytes = 16 << 10

// VerifyILNMessageBytes authenticates one concrete serialized Hyperlane V3
// message, rather than a Merkle leaf or caller-provided message description.
func VerifyILNMessageBytes(
    message []byte,
    expected types.Hash,
    sourceDomain uint32,
    sourceRouter types.Address,
    destinationDomain uint32,
    destinationRouter types.Address,
) error {
    if len(message)<77 || len(message)>MaxILNSerializedMessageBytes ||
       message[0]!=3 ||
       expected==types.ZeroHash || sourceDomain==0 || destinationDomain==0 ||
       sourceRouter==types.ZeroAddress || destinationRouter==types.ZeroAddress {
        return fmt.Errorf("invalid bounded Hyperlane V3 direct message")
    }
    if crypto.Keccak256Hash(message)!=expected {
        return fmt.Errorf("Hyperlane direct message ID does not match authenticated source Dispatch")
    }
    if binary.BigEndian.Uint32(message[5:9])!=sourceDomain ||
       binary.BigEndian.Uint32(message[41:45])!=destinationDomain ||
       !bytes.Equal(message[9:21],make([]byte,12)) ||
       !bytes.Equal(message[45:57],make([]byte,12)) ||
       !bytes.Equal(message[21:41],sourceRouter.Bytes()) ||
       !bytes.Equal(message[57:77],destinationRouter.Bytes()) {
        return fmt.Errorf("Hyperlane direct message sender, origin, destination or recipient differs from canonical route")
    }
    return nil
}
