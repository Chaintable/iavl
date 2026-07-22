package iavl

import (
	"bytes"
	"errors"
	"testing"

	dbm "github.com/cosmos/iavl/db"
	"github.com/cosmos/iavl/internal/encoding"
	"github.com/cosmos/iavl/mock"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
)

func TestInspectLegacyRootsSortedAndOwned(t *testing.T) {
	db := dbm.NewMemDB()
	hash2 := bytes.Repeat([]byte{2}, hashSize)
	hash10 := bytes.Repeat([]byte{10}, hashSize)
	require.NoError(t, db.Set(legacyRootKeyFormat.Key(int64(10)), hash10))
	require.NoError(t, db.Set(legacyRootKeyFormat.Key(int64(3)), []byte{}))
	require.NoError(t, db.Set(legacyRootKeyFormat.Key(int64(2)), hash2))

	var records []LegacyRootRecord
	require.NoError(t, InspectLegacyRoots(db, func(record LegacyRootRecord) error {
		records = append(records, record)
		return nil
	}))
	require.Equal(t, []int64{2, 3, 10}, []int64{records[0].Version, records[1].Version, records[2].Version})
	require.Equal(t, hash2, records[0].Hash)
	require.Empty(t, records[1].Hash)
	require.Equal(t, hash10, records[2].Hash)

	records[0].DBKey[0] = 'x'
	records[0].Hash[0] = 0xff
	var first LegacyRootRecord
	require.NoError(t, InspectLegacyRoots(db, func(record LegacyRootRecord) error {
		if first.DBKey == nil {
			first = record
		}
		return nil
	}))
	require.Equal(t, legacyRootKeyFormat.Key(int64(2)), first.DBKey)
	require.Equal(t, hash2, first.Hash)
}

func TestInspectLegacyNodesSortedLeafAndBranch(t *testing.T) {
	db := dbm.NewMemDB()
	leafHash := bytes.Repeat([]byte{1}, hashSize)
	branchHash := bytes.Repeat([]byte{2}, hashSize)
	leftHash := bytes.Repeat([]byte{3}, hashSize)
	rightHash := bytes.Repeat([]byte{4}, hashSize)
	require.NoError(t, db.Set(legacyNodeKeyFormat.Key(branchHash), encodeLegacyInspectionNode(
		t, 1, 2, 7, []byte("middle"), leftHash, rightHash,
	)))
	require.NoError(t, db.Set(legacyNodeKeyFormat.Key(leafHash), encodeLegacyInspectionNode(
		t, 0, 1, 3, []byte("storage-key"), []byte("storage-value"), nil,
	)))

	var records []LegacyNodeRecord
	require.NoError(t, InspectLegacyNodes(db, func(record LegacyNodeRecord) error {
		records = append(records, record)
		return nil
	}))
	require.Len(t, records, 2)
	require.Equal(t, leafHash, records[0].Hash)
	require.True(t, records[0].IsLeaf())
	require.Equal(t, int64(3), records[0].Version)
	require.Equal(t, []byte("storage-key"), records[0].Key)
	require.Equal(t, []byte("storage-value"), records[0].Value)
	require.Nil(t, records[0].LeftHash)
	require.Nil(t, records[0].RightHash)

	require.Equal(t, branchHash, records[1].Hash)
	require.False(t, records[1].IsLeaf())
	require.Equal(t, int64(7), records[1].Version)
	require.Equal(t, int8(1), records[1].Height)
	require.Equal(t, int64(2), records[1].Size)
	require.Equal(t, []byte("middle"), records[1].Key)
	require.Nil(t, records[1].Value)
	require.Equal(t, leftHash, records[1].LeftHash)
	require.Equal(t, rightHash, records[1].RightHash)

	records[0].Key[0] = 'X'
	records[0].Value[0] = 'X'
	var leafAgain LegacyNodeRecord
	require.NoError(t, InspectLegacyNodes(db, func(record LegacyNodeRecord) error {
		if record.IsLeaf() {
			leafAgain = record
		}
		return nil
	}))
	require.Equal(t, []byte("storage-key"), leafAgain.Key)
	require.Equal(t, []byte("storage-value"), leafAgain.Value)
}

func TestInspectLegacyOrphansSortedAndDecoded(t *testing.T) {
	db := dbm.NewMemDB()
	hashA := bytes.Repeat([]byte{1}, hashSize)
	hashB := bytes.Repeat([]byte{2}, hashSize)
	hashC := bytes.Repeat([]byte{3}, hashSize)
	require.NoError(t, db.Set(legacyOrphanKeyFormat.Key(int64(4), int64(2), hashA), hashA))
	require.NoError(t, db.Set(legacyOrphanKeyFormat.Key(int64(3), int64(1), hashC), hashC))
	require.NoError(t, db.Set(legacyOrphanKeyFormat.Key(int64(4), int64(1), hashB), hashB))

	var records []LegacyOrphanRecord
	require.NoError(t, InspectLegacyOrphans(db, func(record LegacyOrphanRecord) error {
		records = append(records, record)
		return nil
	}))
	require.Len(t, records, 3)
	require.Equal(t, LegacyOrphanRecord{
		DBKey:     legacyOrphanKeyFormat.Key(int64(3), int64(1), hashC),
		ToVersion: 3, FromVersion: 1, Hash: hashC,
	}, records[0])
	require.Equal(t, int64(4), records[1].ToVersion)
	require.Equal(t, int64(1), records[1].FromVersion)
	require.Equal(t, hashB, records[1].Hash)
	require.Equal(t, int64(4), records[2].ToVersion)
	require.Equal(t, int64(2), records[2].FromVersion)
	require.Equal(t, hashA, records[2].Hash)
}

func TestLegacyInspectionReturnsCallbackAndIteratorErrors(t *testing.T) {
	t.Run("callback", func(t *testing.T) {
		db := dbm.NewMemDB()
		require.NoError(t, db.Set(legacyRootKeyFormat.Key(int64(1)), []byte{}))
		callbackErr := errors.New("stop")
		err := InspectLegacyRoots(db, func(LegacyRootRecord) error { return callbackErr })
		require.ErrorIs(t, err, callbackErr)
	})

	t.Run("iterator", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		db := mock.NewMockDB(ctrl)
		iterator := mock.NewMockIterator(ctrl)
		iteratorErr := errors.New("iterator failed")
		db.EXPECT().Iterator([]byte{'r'}, []byte{'s'}).Return(iterator, nil)
		iterator.EXPECT().Valid().Return(false)
		iterator.EXPECT().Error().Return(iteratorErr)
		iterator.EXPECT().Close().Return(nil)

		err := InspectLegacyRoots(db, func(LegacyRootRecord) error { return nil })
		require.ErrorIs(t, err, iteratorErr)
	})

	t.Run("close", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		db := mock.NewMockDB(ctrl)
		iterator := mock.NewMockIterator(ctrl)
		closeErr := errors.New("close failed")
		db.EXPECT().Iterator([]byte{'r'}, []byte{'s'}).Return(iterator, nil)
		iterator.EXPECT().Valid().Return(false)
		iterator.EXPECT().Error().Return(nil)
		iterator.EXPECT().Close().Return(closeErr)

		err := InspectLegacyRoots(db, func(LegacyRootRecord) error { return nil })
		require.ErrorIs(t, err, closeErr)
	})
}

func TestLegacyInspectionRejectsBadRecords(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*testing.T, *dbm.MemDB)
		inspect func(dbm.DB) error
		want    string
	}{
		{
			name: "root key length",
			prepare: func(t *testing.T, db *dbm.MemDB) {
				require.NoError(t, db.Set([]byte{'r', 1}, []byte{}))
			},
			inspect: func(db dbm.DB) error {
				return InspectLegacyRoots(db, func(LegacyRootRecord) error { return nil })
			},
			want: "root key length",
		},
		{
			name: "node trailing bytes",
			prepare: func(t *testing.T, db *dbm.MemDB) {
				hash := bytes.Repeat([]byte{1}, hashSize)
				body := append(encodeLegacyInspectionNode(t, 0, 1, 1, []byte("key"), []byte("value"), nil), 0xff)
				require.NoError(t, db.Set(legacyNodeKeyFormat.Key(hash), body))
			},
			inspect: func(db dbm.DB) error {
				return InspectLegacyNodes(db, func(LegacyNodeRecord) error { return nil })
			},
			want: "trailing bytes",
		},
		{
			name: "branch child hash length",
			prepare: func(t *testing.T, db *dbm.MemDB) {
				hash := bytes.Repeat([]byte{1}, hashSize)
				body := encodeLegacyInspectionNode(t, 1, 2, 1, []byte("key"), []byte{1}, bytes.Repeat([]byte{2}, hashSize))
				require.NoError(t, db.Set(legacyNodeKeyFormat.Key(hash), body))
			},
			inspect: func(db dbm.DB) error {
				return InspectLegacyNodes(db, func(LegacyNodeRecord) error { return nil })
			},
			want: "child hash lengths",
		},
		{
			name: "orphan hash mismatch",
			prepare: func(t *testing.T, db *dbm.MemDB) {
				hash := bytes.Repeat([]byte{1}, hashSize)
				require.NoError(t, db.Set(
					legacyOrphanKeyFormat.Key(int64(2), int64(1), hash),
					bytes.Repeat([]byte{2}, hashSize),
				))
			},
			inspect: func(db dbm.DB) error {
				return InspectLegacyOrphans(db, func(LegacyOrphanRecord) error { return nil })
			},
			want: "invalid hash value",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := dbm.NewMemDB()
			test.prepare(t, db)
			err := test.inspect(db)
			require.ErrorContains(t, err, test.want)
		})
	}
}

func encodeLegacyInspectionNode(
	t *testing.T,
	height, size, version int64,
	key, firstBody, secondBody []byte,
) []byte {
	t.Helper()
	var body bytes.Buffer
	require.NoError(t, encoding.EncodeVarint(&body, height))
	require.NoError(t, encoding.EncodeVarint(&body, size))
	require.NoError(t, encoding.EncodeVarint(&body, version))
	require.NoError(t, encoding.EncodeBytes(&body, key))
	require.NoError(t, encoding.EncodeBytes(&body, firstBody))
	if height != 0 {
		require.NoError(t, encoding.EncodeBytes(&body, secondBody))
	}
	return body.Bytes()
}
