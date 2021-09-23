package channeldb

import (
	"errors"
	"fmt"

	"github.com/lightningnetwork/lnd/kvdb"
)

var (
	// metaBucket stores all the meta information concerning the state of
	// the database.
	metaBucket = []byte("metadata")

	// dbVersionKey is a boltdb key and it's used for storing/retrieving
	// current database version.
	dbVersionKey = []byte("dbp")

	// TombstoneKey is the key under which we add a tag in the source DB
	// after we've successfully and completely migrated it to the target/
	// destination DB.
	TombstoneKey = []byte("data-migration-tombstone")

	// ErrMarkerNotPresent is the error that is returned if the queried
	// marker is not present in the given database.
	ErrMarkerNotPresent = errors.New("marker not present")
)

// Meta structure holds the database meta information.
type Meta struct {
	// DbVersionNumber is the current schema version of the database.
	DbVersionNumber uint32
}

// FetchMeta fetches the meta data from boltdb and returns filled meta
// structure.
func (d *DB) FetchMeta(tx kvdb.RTx) (*Meta, error) {
	var meta *Meta

	err := kvdb.View(d, func(tx kvdb.RTx) error {
		return FetchMeta(meta, tx)
	}, func() {
		meta = &Meta{}
	})
	if err != nil {
		return nil, err
	}

	return meta, nil
}

// FetchMeta is a helper function used in order to allow callers to re-use a
// database transaction. See the publicly exported FetchMeta method for more
// information.
func FetchMeta(meta *Meta, tx kvdb.RTx) error {
	metaBucket := tx.ReadBucket(metaBucket)
	if metaBucket == nil {
		return ErrMetaNotFound
	}

	data := metaBucket.Get(dbVersionKey)
	if data == nil {
		meta.DbVersionNumber = getLatestDBVersion(dbVersions)
	} else {
		meta.DbVersionNumber = byteOrder.Uint32(data)
	}

	return nil
}

// PutMeta writes the passed instance of the database met-data struct to disk.
func (d *DB) PutMeta(meta *Meta) error {
	return kvdb.Update(d, func(tx kvdb.RwTx) error {
		return putMeta(meta, tx)
	}, func() {})
}

// putMeta is an internal helper function used in order to allow callers to
// re-use a database transaction. See the publicly exported PutMeta method for
// more information.
func putMeta(meta *Meta, tx kvdb.RwTx) error {
	metaBucket, err := tx.CreateTopLevelBucket(metaBucket)
	if err != nil {
		return err
	}

	return putDbVersion(metaBucket, meta)
}

func putDbVersion(metaBucket kvdb.RwBucket, meta *Meta) error {
	scratch := make([]byte, 4)
	byteOrder.PutUint32(scratch, meta.DbVersionNumber)
	return metaBucket.Put(dbVersionKey, scratch)
}

// CheckMarkerPresent returns the marker under the requested key or
// ErrMarkerNotFound if either the root bucket or the marker key within that
// bucket does not exist.
func CheckMarkerPresent(tx kvdb.RTx, markerKey []byte) ([]byte, error) {
	markerBucket := tx.ReadBucket(markerKey)
	if markerBucket == nil {
		return nil, ErrMarkerNotPresent
	}

	key := markerBucket.Get(markerKey)
	if len(key) == 0 {
		return nil, ErrMarkerNotPresent
	}

	return key, nil
}

// EnsureNoTombstone returns an error if there is a tombstone marker in the DB
// of the given transaction.
func EnsureNoTombstone(tx kvdb.RTx) error {
	marker, err := CheckMarkerPresent(tx, TombstoneKey)
	if err == ErrMarkerNotPresent {
		// No marker present, so no tombstone. The DB is still alive.
		return nil
	}
	if err != nil {
		return err
	}

	// There was no error so there is a tombstone marker/tag. We cannot use
	// this DB anymore.
	return fmt.Errorf("refusing to use db, it was marked with a tombstone "+
		"after successful data migration; tombstone reads: %s",
		string(marker))
}

// AddMarker adds the marker with the given key into a top level bucket with the
// same name. So the structure will look like:
// marker-key (top level bucket)
//    |->   marker-key:marker-value (key/value pair)
func AddMarker(tx kvdb.RwTx, markerKey, markerValue []byte) error {
	markerBucket, err := tx.CreateTopLevelBucket(markerKey)
	if err != nil {
		return err
	}

	return markerBucket.Put(markerKey, markerValue)
}
