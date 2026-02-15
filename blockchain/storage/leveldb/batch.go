package leveldb

import (
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/xgr-network/xgr-node/blockchain/storage"
)

var _ storage.Batch = (*batchLevelDB)(nil)

type batchLevelDB struct {
	db *leveldb.DB
	b  *leveldb.Batch
}

func NewBatchLevelDB(db *leveldb.DB) *batchLevelDB {
	return &batchLevelDB{
		db: db,
		b:  new(leveldb.Batch),
	}
}

func (b *batchLevelDB) Delete(key []byte) {
	b.b.Delete(key)
}

func (b *batchLevelDB) Put(k []byte, v []byte) {
	b.b.Put(k, v)
}

func (b *batchLevelDB) Write() error {
	return b.db.Write(b.b, nil)
}
