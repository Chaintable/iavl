package iavl

import (
	"bytes"
	"errors"
	"fmt"
	"math"

	dbm "github.com/cosmos/iavl/db"
	"github.com/cosmos/iavl/internal/encoding"
)

// LegacyRootRecord describes one r<version> entry in the legacy IAVL schema.
// All byte slices are owned by the record and remain valid after the callback returns.
type LegacyRootRecord struct {
	DBKey   []byte
	Version int64
	Hash    []byte
}

// LegacyNodeRecord describes one n<hash> entry in the legacy IAVL schema.
// All byte slices are owned by the record and remain valid after the callback returns.
type LegacyNodeRecord struct {
	DBKey     []byte
	Hash      []byte
	Version   int64
	Height    int8
	Size      int64
	Key       []byte
	Value     []byte
	LeftHash  []byte
	RightHash []byte
}

// IsLeaf reports whether the record is a legacy leaf node.
func (record LegacyNodeRecord) IsLeaf() bool {
	return record.Height == 0
}

// LegacyOrphanRecord describes one o<to-version><from-version><hash> entry in
// the legacy IAVL schema. All byte slices are owned by the record and remain
// valid after the callback returns.
type LegacyOrphanRecord struct {
	DBKey       []byte
	ToVersion   int64
	FromVersion int64
	Hash        []byte
}

// InspectLegacyRoots visits legacy roots in ascending database-key order.
func InspectLegacyRoots(db dbm.DB, callback func(LegacyRootRecord) error) error {
	if callback == nil {
		return errors.New("legacy root callback cannot be nil")
	}
	return inspectLegacyPrefix(db, legacyRootKeyFormat.Key(), func(key, value []byte) error {
		if len(key) != legacyRootKeyFormat.Length() {
			return fmt.Errorf("invalid legacy root key length %d", len(key))
		}
		var version int64
		legacyRootKeyFormat.Scan(key, &version)
		if version <= 0 {
			return fmt.Errorf("invalid legacy root version %d", version)
		}
		if len(value) != 0 && len(value) != hashSize {
			return fmt.Errorf("legacy root version %d has hash length %d", version, len(value))
		}
		record := LegacyRootRecord{
			DBKey: bytes.Clone(key), Version: version, Hash: bytes.Clone(value),
		}
		if err := callback(record); err != nil {
			return fmt.Errorf("legacy root version %d callback: %w", version, err)
		}
		return nil
	})
}

// InspectLegacyNodes visits legacy nodes in ascending database-key order.
func InspectLegacyNodes(db dbm.DB, callback func(LegacyNodeRecord) error) error {
	if callback == nil {
		return errors.New("legacy node callback cannot be nil")
	}
	return inspectLegacyPrefix(db, legacyNodeKeyFormat.Prefix(), func(key, value []byte) error {
		record, err := decodeLegacyNodeRecord(key, value)
		if err != nil {
			return err
		}
		if err := callback(record); err != nil {
			return fmt.Errorf("legacy node %x callback: %w", record.Hash, err)
		}
		return nil
	})
}

// InspectLegacyOrphans visits legacy orphan entries in ascending database-key order.
func InspectLegacyOrphans(db dbm.DB, callback func(LegacyOrphanRecord) error) error {
	if callback == nil {
		return errors.New("legacy orphan callback cannot be nil")
	}
	return inspectLegacyPrefix(db, legacyOrphanKeyFormat.Key(), func(key, value []byte) error {
		if len(key) != legacyOrphanKeyFormat.Length() {
			return fmt.Errorf("invalid legacy orphan key length %d", len(key))
		}
		var toVersion, fromVersion int64
		var hash []byte
		legacyOrphanKeyFormat.Scan(key, &toVersion, &fromVersion, &hash)
		if fromVersion <= 0 || toVersion < fromVersion {
			return fmt.Errorf("invalid legacy orphan lifetime %d-%d", fromVersion, toVersion)
		}
		if len(value) != hashSize || !bytes.Equal(value, hash) {
			return fmt.Errorf("legacy orphan %d-%d has invalid hash value", fromVersion, toVersion)
		}
		record := LegacyOrphanRecord{
			DBKey: bytes.Clone(key), ToVersion: toVersion, FromVersion: fromVersion, Hash: bytes.Clone(hash),
		}
		if err := callback(record); err != nil {
			return fmt.Errorf("legacy orphan %d-%d callback: %w", fromVersion, toVersion, err)
		}
		return nil
	})
}

func inspectLegacyPrefix(db dbm.DB, prefix []byte, callback func(key, value []byte) error) (returnErr error) {
	if db == nil {
		return errors.New("legacy inspection database cannot be nil")
	}
	start := bytes.Clone(prefix)
	end := bytes.Clone(prefix)
	end[len(end)-1]++
	iterator, err := db.Iterator(start, end)
	if err != nil {
		return err
	}
	defer func() {
		returnErr = errors.Join(returnErr, iterator.Close())
	}()
	for ; iterator.Valid(); iterator.Next() {
		if err := callback(iterator.Key(), iterator.Value()); err != nil {
			return err
		}
	}
	if err := iterator.Error(); err != nil {
		return fmt.Errorf("legacy inspection iterator: %w", err)
	}
	return nil
}

func decodeLegacyNodeRecord(dbKey, body []byte) (LegacyNodeRecord, error) {
	if len(dbKey) != legacyNodeKeyFormat.Length() {
		return LegacyNodeRecord{}, fmt.Errorf("invalid legacy node key length %d", len(dbKey))
	}
	hash := dbKey[1:]
	rest := body
	height, err := consumeLegacyVarint(&rest, "height")
	if err != nil {
		return LegacyNodeRecord{}, err
	}
	if height < 0 || height > math.MaxInt8 {
		return LegacyNodeRecord{}, fmt.Errorf("invalid legacy node height %d", height)
	}
	size, err := consumeLegacyVarint(&rest, "size")
	if err != nil {
		return LegacyNodeRecord{}, err
	}
	if size < 1 {
		return LegacyNodeRecord{}, fmt.Errorf("invalid legacy node size %d", size)
	}
	version, err := consumeLegacyVarint(&rest, "version")
	if err != nil {
		return LegacyNodeRecord{}, err
	}
	if version <= 0 {
		return LegacyNodeRecord{}, fmt.Errorf("invalid legacy node version %d", version)
	}
	key, err := consumeLegacyBytes(&rest, "key")
	if err != nil {
		return LegacyNodeRecord{}, err
	}
	record := LegacyNodeRecord{
		DBKey: bytes.Clone(dbKey), Hash: bytes.Clone(hash), Version: version,
		Height: int8(height), Size: size, Key: bytes.Clone(key),
	}
	if record.IsLeaf() {
		if size != 1 {
			return LegacyNodeRecord{}, fmt.Errorf("legacy leaf node has size %d", size)
		}
		value, err := consumeLegacyBytes(&rest, "value")
		if err != nil {
			return LegacyNodeRecord{}, err
		}
		record.Value = bytes.Clone(value)
	} else {
		if size < 2 {
			return LegacyNodeRecord{}, fmt.Errorf("legacy branch node has size %d", size)
		}
		leftHash, err := consumeLegacyBytes(&rest, "left hash")
		if err != nil {
			return LegacyNodeRecord{}, err
		}
		rightHash, err := consumeLegacyBytes(&rest, "right hash")
		if err != nil {
			return LegacyNodeRecord{}, err
		}
		if len(leftHash) != hashSize || len(rightHash) != hashSize {
			return LegacyNodeRecord{}, fmt.Errorf("legacy branch node has child hash lengths %d/%d", len(leftHash), len(rightHash))
		}
		record.LeftHash, record.RightHash = bytes.Clone(leftHash), bytes.Clone(rightHash)
	}
	if len(rest) != 0 {
		return LegacyNodeRecord{}, fmt.Errorf("legacy node %x has %d trailing bytes", hash, len(rest))
	}
	return record, nil
}

func consumeLegacyVarint(body *[]byte, field string) (int64, error) {
	value, read, err := encoding.DecodeVarint(*body)
	if err != nil {
		return 0, fmt.Errorf("decode legacy node %s: %w", field, err)
	}
	*body = (*body)[read:]
	return value, nil
}

func consumeLegacyBytes(body *[]byte, field string) ([]byte, error) {
	value, read, err := encoding.DecodeBytes(*body)
	if err != nil {
		return nil, fmt.Errorf("decode legacy node %s: %w", field, err)
	}
	*body = (*body)[read:]
	return value, nil
}
