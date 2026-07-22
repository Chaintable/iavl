package iavl

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	dbm "github.com/cosmos/iavl/db"
)

// TestDiffRoundTrip generate random change sets, build an iavl tree versions,
// then extract state changes from the versions and compare with the original change sets.
func TestDiffRoundTrip(t *testing.T) {
	changeSets := genChangeSets(rand.New(rand.NewSource(0)), 300)

	// apply changeSets to tree
	db := dbm.NewMemDB()
	tree := NewMutableTree(db, 0, true, NewNopLogger())
	for i := range changeSets {
		v, err := tree.SaveChangeSet(changeSets[i])
		require.NoError(t, err)
		require.Equal(t, int64(i+1), v)
	}

	// extract change sets from db
	var extractChangeSets []*ChangeSet
	tree2 := NewImmutableTree(db, 0, true, NewNopLogger())
	err := tree2.TraverseStateChanges(0, math.MaxInt64, func(version int64, changeSet *ChangeSet) error {
		extractChangeSets = append(extractChangeSets, changeSet)
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, changeSets, extractChangeSets)
}

func TestDiffRangeMatchesFilteredFullDiff(t *testing.T) {
	changeSets := genChangeSets(rand.New(rand.NewSource(1)), 40)
	db := dbm.NewMemDB()
	tree := NewMutableTree(db, 0, true, NewNopLogger())
	for i := range changeSets {
		_, err := tree.SaveChangeSet(changeSets[i])
		require.NoError(t, err)
	}

	startKey, endKey := []byte("test-200"), []byte("test-700")
	immutable := NewImmutableTree(db, 0, true, NewNopLogger())
	var expected []*ChangeSet
	err := immutable.TraverseStateChanges(1, int64(len(changeSets)), func(_ int64, changeSet *ChangeSet) error {
		filtered := &ChangeSet{}
		for _, pair := range changeSet.Pairs {
			if bytes.Compare(pair.Key, startKey) >= 0 && bytes.Compare(pair.Key, endKey) < 0 {
				filtered.Pairs = append(filtered.Pairs, pair)
			}
		}
		expected = append(expected, filtered)
		return nil
	})
	require.NoError(t, err)

	var actual []*ChangeSet
	err = immutable.TraverseStateChangesInKeyRange(
		1, int64(len(changeSets)), startKey, endKey,
		func(_ int64, changeSet *ChangeSet) error {
			actual = append(actual, changeSet)
			return nil
		},
	)
	require.NoError(t, err)
	require.Equal(t, expected, actual)
}

func TestDiffRangeHonorsHalfOpenBounds(t *testing.T) {
	db := dbm.NewMemDB()
	tree := NewMutableTree(db, 0, true, NewNopLogger())
	_, err := tree.SaveChangeSet(&ChangeSet{Pairs: []*KVPair{
		{Key: []byte{0x01}, Value: []byte("before")},
		{Key: []byte{0x02}, Value: []byte("start")},
		{Key: []byte{0x02, 0xff}, Value: []byte("inside")},
		{Key: []byte{0x03}, Value: []byte("end")},
	}})
	require.NoError(t, err)

	immutable := NewImmutableTree(db, 0, true, NewNopLogger())
	var got *ChangeSet
	err = immutable.TraverseStateChangesInKeyRange(1, 1, []byte{0x02}, []byte{0x03}, func(_ int64, changeSet *ChangeSet) error {
		got = changeSet
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, &ChangeSet{Pairs: []*KVPair{
		{Key: []byte{0x02}, Value: []byte("start")},
		{Key: []byte{0x02, 0xff}, Value: []byte("inside")},
	}}, got)

	_, err = tree.SaveChangeSet(&ChangeSet{Pairs: []*KVPair{
		{Delete: true, Key: []byte{0x02}},
		{Key: []byte{0x02, 0xff}, Value: []byte("updated")},
		{Key: []byte{0x03}, Value: []byte("outside-updated")},
	}})
	require.NoError(t, err)
	immutable = NewImmutableTree(db, 0, true, NewNopLogger())
	err = immutable.TraverseStateChangesInKeyRange(2, 2, []byte{0x02}, []byte{0x03}, func(_ int64, changeSet *ChangeSet) error {
		got = changeSet
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, &ChangeSet{Pairs: []*KVPair{
		{Delete: true, Key: []byte{0x02}},
		{Key: []byte{0x02, 0xff}, Value: []byte("updated")},
	}}, got)

	err = immutable.TraverseStateChangesInKeyRange(1, 1, []byte{0x03}, []byte{0x02}, func(int64, *ChangeSet) error { return nil })
	require.ErrorContains(t, err, "invalid state change key range")
}

type getCountingDB struct {
	dbm.DB
	gets int
}

func (db *getCountingDB) Get(key []byte) ([]byte, error) {
	db.gets++
	return db.DB.Get(key)
}

func TestDiffRangePrunesExcludedSubtrees(t *testing.T) {
	db := &getCountingDB{DB: dbm.NewMemDB()}
	tree := NewMutableTree(db, 0, true, NewNopLogger())
	for version := byte(1); version <= 2; version++ {
		changeSet := &ChangeSet{}
		for prefix := byte(1); prefix <= 3; prefix++ {
			for suffix := 0; suffix < 128; suffix++ {
				changeSet.Pairs = append(changeSet.Pairs, &KVPair{
					Key: []byte{prefix, byte(suffix)}, Value: []byte{version},
				})
			}
		}
		_, err := tree.SaveChangeSet(changeSet)
		require.NoError(t, err)
	}

	db.gets = 0
	immutable := NewImmutableTree(db, 0, true, NewNopLogger())
	var full *ChangeSet
	err := immutable.TraverseStateChanges(2, 2, func(_ int64, changeSet *ChangeSet) error {
		full = changeSet
		return nil
	})
	require.NoError(t, err)
	fullGets := db.gets

	db.gets = 0
	immutable = NewImmutableTree(db, 0, true, NewNopLogger())
	var ranged *ChangeSet
	err = immutable.TraverseStateChangesInKeyRange(2, 2, []byte{0x02}, []byte{0x03}, func(_ int64, changeSet *ChangeSet) error {
		ranged = changeSet
		return nil
	})
	require.NoError(t, err)
	rangedGets := db.gets

	expected := &ChangeSet{}
	for _, pair := range full.Pairs {
		if keyInRange(pair.Key, []byte{0x02}, []byte{0x03}) {
			expected.Pairs = append(expected.Pairs, pair)
		}
	}
	require.Equal(t, expected, ranged)
	require.Less(t, rangedGets, fullGets)
}

func genChangeSets(r *rand.Rand, n int) []*ChangeSet {
	var changeSets []*ChangeSet

	for i := 0; i < n; i++ {
		items := make(map[string]*KVPair)
		start, count, step := r.Int63n(1000), r.Int63n(1000), r.Int63n(10)
		for i := start; i < start+count*step; i += step {
			value := make([]byte, 8)
			binary.LittleEndian.PutUint64(value, uint64(i))

			key := fmt.Sprintf("test-%d", i)
			items[key] = &KVPair{
				Key:   []byte(key),
				Value: value,
			}
		}
		if len(changeSets) > 0 {
			// pick some random keys to delete from the last version
			lastChangeSet := changeSets[len(changeSets)-1]
			count = r.Int63n(10)
			for _, pair := range lastChangeSet.Pairs {
				if count <= 0 {
					break
				}
				if pair.Delete {
					continue
				}
				items[string(pair.Key)] = &KVPair{
					Key:    pair.Key,
					Delete: true,
				}
				count--
			}

			// Special case, set to identical value
			if len(lastChangeSet.Pairs) > 0 {
				i := r.Int63n(int64(len(lastChangeSet.Pairs)))
				pair := lastChangeSet.Pairs[i]
				if !pair.Delete {
					items[string(pair.Key)] = &KVPair{
						Key:   pair.Key,
						Value: pair.Value,
					}
				}
			}
		}

		var keys []string
		for key := range items {
			keys = append(keys, key)
		}
		sort.Strings(keys)

		var cs ChangeSet
		for _, key := range keys {
			cs.Pairs = append(cs.Pairs, items[key])
		}

		changeSets = append(changeSets, &cs)
	}
	return changeSets
}
